package main

import (
	"log"
	"os"

	ichihime_http "github.com/gammbol/ichihime/internal/http"

	"github.com/joho/godotenv"
)

var client ichihime_http.IchihimeCfg

func main() {
	dotenvErr := godotenv.Load()
	if dotenvErr != nil {
		log.Fatal("Error loading .env file")
	}

	client.Init(os.Getenv("DATABASE_URL"), os.Getenv("API_URL"))
	// client.AddRoute("/albums", ichihime_http.TypeGet, GetAlbums)
	client.Run()
}

