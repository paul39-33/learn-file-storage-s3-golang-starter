package main

import (
	"net/http"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
	"io"
	"os"
	"mime"
	"crypto/rand"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"encoding/hex"
	"os/exec"
	"bytes"
	"encoding/json"
	"log"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	//set max upload limit of 1 GB (1 << 30 bytes)
	const maxMemory = 1 << 30
	r.ParseMultipartForm(maxMemory)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading video ", videoID, "by user ", userID)

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "couldn't get video data", err)
		return
	}

	//check if video's user ID matches with the req's user ID
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "video access unauthorized", nil)
		return
	}

	//parse the uploaded video file from the form data
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "problem parsing video data", err)
		return
	}
	defer file.Close()

	mediaType := header.Header.Get("Content-Type")
	if mediaType == "" {
		respondWithError(w, http.StatusBadRequest, "missing Content-Type", nil)
		return
	}

	mimeType, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "problem getting mime type", err)
		return
	}
	if mimeType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "file format not supported", nil)
		return
	}

	//save the uploaded file to a temp file on disk
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "problem creating temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err = io.Copy(tempFile, file); err != nil {
		respondWithError(w, http.StatusBadRequest, "problem copying file to temp file", err)
		return
	}

	//reset temp file's file pointer to the beginning
	if _, err = tempFile.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusBadRequest, "problem resetting temp file pointer", err)
		return
	}

	//generate unique name for the video file
	videoName := make([]byte, 32)
	rand.Read(videoName)
	keyNameEncoded := hex.EncodeToString(videoName)

	//get the aspect ratio from the temp file thats saved to disk
	videoAspect, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "problem getting video aspect", err)
		return
	}
	var key string
	if videoAspect == "16:9" {
		key = fmt.Sprintf("landscape/%v.mp4", keyNameEncoded)
	} else if videoAspect == "9:16" {
		key = fmt.Sprintf("portrait/%v.mp4", keyNameEncoded)
	} else {
		key = fmt.Sprintf("other/%v.mp4", keyNameEncoded)
	}

	//change the video processing by moving the moov to the beginning
	processedPath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "problem processing video for fast start", err)
		return
	}
	defer os.Remove(processedPath)

	//open the processedPath since s3 Body expect an io.Reader
	f, err := os.Open(processedPath)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "problem opening processed file", err)
		return
	}
	defer f.Close()

	//put the object into s3 using PutObject
	PutObjectInput := &s3.PutObjectInput {
		Bucket:		 aws.String(cfg.s3Bucket),
		Key:		 aws.String(key),
		Body:		 f,
		ContentType: aws.String(mimeType),
	}

	_, err = cfg.s3Client.PutObject(r.Context(), PutObjectInput)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "problem putting object into s3", err)
		return
	}

	//update videoURL in video field to contain s3 URL with format https://<bucket-name>.s3.<region>.amazonaws.com/<key>
	videoURL := fmt.Sprintf("https://%v.s3.%v.amazonaws.com/%v", cfg.s3Bucket, cfg.s3Region, key)
	video.VideoURL = &videoURL

	//update the video data in database
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "error updating video records", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}


func getVideoAspectRatio(filePath string) (string, error) {
	//run the ffprobe command
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	//set the resulting cmd's Stdout field to a pointer to a new bytes.Buffer
	var b bytes.Buffer
	cmd.Stdout = &b
	//run the command
	err := cmd.Run()
	if err != nil {
		log.Fatalf("problem running cmd")
		return "", err
	}
	
	//unmarshal the cmd stdout from buffer's .Bytes into a JSON struct
	type parameters struct {
		Streams []struct {
			Width	int `json:"width,omitempty"`
			Height	int `json:"height,omitempty"`
		} `json:"streams"`
	}

	fileInfo := parameters{}
	err = json.Unmarshal(b.Bytes(), &fileInfo)
	if err != nil || len(fileInfo.Streams) == 0 {
		log.Fatalf("problem during unmarshal")
		return "", err
	}

	ratio := fileInfo.Streams[0].Width / fileInfo.Streams[0].Height

	if ratio == (16/9) {
		return "16:9", nil
	} else if ratio == (9/16) {
		return "9:16", nil
	} else {
		return "other", nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := filePath + ".processing"

	cmd := exec.Command(
		"ffmpeg", "-i", filePath,
		"-c", "copy", 
		"-movflags", "faststart", 
		"-f", "mp4", outputFilePath,
	)
	
	err := cmd.Run()
	if err != nil {
		log.Fatalf("problem running exec command:%v ", err)
		return "", err
	}

	return outputFilePath, nil
}