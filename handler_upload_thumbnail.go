package main

import (
	"fmt"
	"net/http"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
	"io"
	"path/filepath"
	"os"
	"strings"
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

	//store the file into file system in /assets/<videoID>.<file_extension>
	fileExt := strings.Split(mediaType, "/") // returns ["image", "png"]
	filename := fmt.Sprintf("%v.%v", videoIDString, fileExt[1])
	filePath := filepath.Join(cfg.assetsRoot, filename)
	newFile, err := os.Create(filePath)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "error creating file", err)
		return
	}

	if _, err := io.Copy(newFile, file); err != nil {
		respondWithError(w, http.StatusBadRequest, "error copying file to file system", err)
		return
	}

	//create thumbnail URL
	thumbnailURL := fmt.Sprintf("http://localhost:%v/assets/%v.%v", cfg.port, videoIDString, fileExt[1])

	//update video metadata to the new thumbnail URL
	video.ThumbnailURL = &thumbnailURL

	//update the video record in the database
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "error updating video records", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
