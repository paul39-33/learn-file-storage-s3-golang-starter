package main

import (
	"fmt"
	"net/http"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
	"io"
	"encoding/base64"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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


	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	//get the image from HTML form and make sure it matched "thumbnail" input name
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType := header.Header.Get("Content-Type")
	if mediaType == "" {
		respondWithError(w, http.StatusBadRequest, "missing Content-Type", nil)
		return
	}

	//read all image data into a byte slice
	data, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to read file", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to get video data", err)
		return
	}

	//check if the authenticated user is the video owner
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "video access unauthorized", nil)
		return
	}

	//create data URL
	fileData := base64.StdEncoding.EncodeToString(data)
	dataURL := fmt.Sprintf("data:%v;base64,%v", mediaType, fileData)

	//update video metadata to the new thumbnail URL
	video.ThumbnailURL = &dataURL

	//update the video record in the database
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "error updating video records", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
