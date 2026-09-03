package router

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

type MethodType int

const (
	TypeGet MethodType = iota
	TypePost
)

type RouterApp struct {
	runString string
	Router *gin.Engine
}


func New(rs string) *RouterApp {
	ra := RouterApp{Router: gin.Default(), runString: rs}
	ra.Router.Use(ErrorHandler())
	return &ra
}

func (this *RouterApp) AddRoute(path string, method MethodType, fn func(*gin.Context)) error {
	switch method {
	case TypeGet:
		this.Router.GET(path, fn)

	case TypePost:
		this.Router.POST(path, fn)

	default:
		return fmt.Errorf("unknown http method")
	}

	return nil
}

func (this *RouterApp) Run() {
	this.Router.Run(this.runString)
}

// Handlers
func ErrorHandler() gin.HandlerFunc {
	return func (c *gin.Context) {
		c.Next()

		if len(c.Errors) > 0 {
			err := c.Errors.Last().Err

			c.IndentedJSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": err.Error(),
			})
		}
	}
}