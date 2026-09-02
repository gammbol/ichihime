package ichihime_http

import (
	"fmt"
	"net/http"

	"github.com/gammbol/ichihime/internal/db"
	"github.com/gin-gonic/gin"
)

type MethodType int

const (
	TypeGet MethodType = iota
	TypePost
)

type IchihimeCfg struct {
	runString string
	router *gin.Engine

	conn db.ConnConf
}

func (h *IchihimeCfg) Init(cs string, rs string) {
	h.runString = rs

	h.router = gin.Default()
	h.conn.Init(cs)

	h.router.GET("/albums", h.GetAlbums)
}

func (h *IchihimeCfg) AddRoute(path string, method MethodType, fn func(*gin.Context)) error {
	switch method {
	case TypeGet:
		h.router.GET(path, fn)

	case TypePost:
		h.router.POST(path, fn)

	default:
		return fmt.Errorf("unknown http method")
	}

	return nil
}

func (h *IchihimeCfg) Run() {
	h.router.Run(h.runString)
}


// Callbacks
func (h *IchihimeCfg) GetAlbums(c *gin.Context) {
	albums, err := h.conn.GetAllAlbums()
		if err != nil {
			fmt.Println(err)
			return
		}

		c.IndentedJSON(http.StatusOK, albums)
}