package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gammbol/ichihime/internal/db"
	"github.com/gammbol/ichihime/internal/db/postgresdb"
	"github.com/gammbol/ichihime/internal/router"
	"github.com/gammbol/ichihime/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

type Application struct {
	db db.DBContract
	routerApp *router.RouterApp

	srv *http.Server
}


func main() {
	dotenvErr := godotenv.Load()
	if dotenvErr != nil {
		log.Fatal("Error loading .env file")
	}

	// client.Init(os.Getenv("DATABASE_URL"), os.Getenv("API_URL"))
	// // client.AddRoute("/albums", ichihime_http.TypeGet, GetAlbums)
	// client.Run()

	db, _ := postgresdb.New(os.Getenv("DATABASE_URL"))
	rApp := router.New(os.Getenv("API_URL"))

	app := NewApplication(db, rApp)

	app.Run()
}

func NewApplication(db db.DBContract, rApp *router.RouterApp) *Application {
	app := Application{
		db: db, 
		routerApp: rApp,
		srv: &http.Server{
			Addr:			":8080",
			Handler:	rApp.Router.Handler(),
		},
	}

	app.AddRouterHandler("/accounts", router.TypeGet, func (c *gin.Context) {
		res, resErr := app.db.GetAllAccounts()
		if resErr != nil {
			c.Error(resErr)
			return
		}

		c.IndentedJSON(http.StatusOK, res)
	})
	app.AddRouterHandler("/accounts/:id", router.TypeGet, func (c *gin.Context) {
		id, idErr := strconv.Atoi(c.Param("id"))
		if idErr != nil {
			c.Status(http.StatusBadRequest)
			c.Error(idErr)
			return
		}

		res, resErr := app.db.GetAccountById(id)
		if resErr != nil {
			c.Status(http.StatusNotFound)
			c.Error(resErr)
			return
		}

		c.IndentedJSON(http.StatusOK, res)
	})
	app.AddRouterHandler("/transfers", router.TypeGet, func (c *gin.Context) {
		res, resErr := app.db.GetAllTransfers()
		if resErr != nil {
			c.Status(http.StatusNotFound)
			c.Error(resErr)
			return
		}

		c.IndentedJSON(http.StatusOK, res)
	})
	app.AddRouterHandler("/transfers/:id", router.TypeGet, func (c *gin.Context) {
		id, idErr := strconv.Atoi(c.Param("id"))
		if idErr != nil {
			c.Status(http.StatusBadRequest)
			c.Error(idErr)
			return
		}

		res, resErr := app.db.GetTransferById(id)
		if resErr != nil {
			c.Status(http.StatusNotFound)
			c.Error(resErr)
			return
		}

		c.IndentedJSON(http.StatusOK, res)
	})

	app.AddRouterHandler("/pay", router.TypePost, func (c *gin.Context) {
		var transferForm storage.TransferForm
		if err := c.ShouldBind(&transferForm); err != nil {
			c.Status(http.StatusBadRequest)
			c.Error(err)
		}

		res, transferErr := app.db.Transfer(transferForm)
		if transferErr != nil {
			c.Status(http.StatusInternalServerError)
			c.Error(transferErr)
			return
		}

		c.IndentedJSON(http.StatusOK, res)
	})

	return &app
}

func (this *Application) Run() {
	// this.routerApp.Run()
	defer this.db.Close()

	go func() {
		// service connection
		if err := this.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	
	// syscall.SIGKILL cannot be caught, so don't need add it
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down the server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := this.srv.Shutdown(ctx); err != nil {
		log.Println("server shutdown:", err)
	}
	log.Println("exiting...")
}


// Callbacks
func (this *Application) AddRouterHandler(path string, method router.MethodType, fn func (*gin.Context)) {
	this.routerApp.AddRoute(path, method, fn)
}


