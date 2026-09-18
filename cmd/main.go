package main

import (
	"context"
	"errors"
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
	uuidcache "github.com/gammbol/ichihime/internal/uuid_cache"
	rediscache "github.com/gammbol/ichihime/internal/uuid_cache/redis"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

type Application struct {
	ctx					context.Context
	db 					db.DBContract
	uuid_cache	uuidcache.UUIDCacheContract
	routerApp 	*router.RouterApp

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
	ctx := context.Background()
	db := postgresdb.New(os.Getenv("DATABASE_URL"), ctx)
	uuid_cache := rediscache.New(os.Getenv("UUID_CACHE_URL"), ctx)
	rApp := router.New(os.Getenv("API_URL"))

	app := NewApplication(
		db,
		uuid_cache,
		rApp,
	)

	app.Run()
}

func NewApplication(db db.DBContract, uuid_cache uuidcache.UUIDCacheContract, rApp *router.RouterApp) *Application {
	app := Application{
		ctx: context.Background(),
		db: db, 
		uuid_cache: uuid_cache,
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
			return
		}

		idempotencyKey := storage.Idempotency{}
		if err := c.ShouldBindHeader(&idempotencyKey); err != nil {
			c.Status(http.StatusBadRequest)
			c.Error(errors.New("No bitches? т_т"))
			return
		}

		print(idempotencyKey.Key)
		idempotencyKey.Status = "pending"
		idempotencyRes, idempotencyErr := uuid_cache.SetNx(idempotencyKey)
		if idempotencyErr != nil {
			c.Status(http.StatusInternalServerError)
			c.Error(idempotencyErr)
			return
		}

		// if idempotencyErr == redis.Nil {

		// 	idempotencyErr := uuid_cache.Set(idempotencyKey)
		// 	if idempotencyErr != nil {
		// 		c.Status(http.StatusInternalServerError)
		// 		c.Error(idempotencyErr)
		// 		return
		// 	}

		// 	res, transferErr := app.db.Transfer(transferForm)
		// 	if transferErr != nil {
		// 		idempotencyKey.Status = "failed"
		// 		uuid_cache.Set(idempotencyKey)
		// 		c.Status(http.StatusInternalServerError)
		// 		c.Error(transferErr)
		// 		return
		// 	}

		// 	idempotencyKey.Status = "completed"
		// 	uuid_cache.Set(idempotencyKey)
		// 	c.IndentedJSON(http.StatusOK, res)
		// 	return
		// }

		if !idempotencyRes {
			uuid_cache.Get(&idempotencyKey)
			switch idempotencyKey.Status {
			case "pending":
				c.Status(http.StatusConflict)
				c.Error(errors.New("Your request is already in process!"))
				return 
			case "completed":
				c.Status(http.StatusConflict)
				c.Error(errors.New("Your request is already processed!"))
				return
			case "failed":
				c.Status(http.StatusBadRequest)
				c.Error(errors.New("Your request has been failed! Please try again!"))
				return

			default:
				c.Status(http.StatusInternalServerError)
				c.Error(errors.New("Oops... Something went wrong!"))
				return
			}
		}

		res, transferErr := app.db.Transfer(transferForm)
		if transferErr != nil {
			idempotencyKey.Status = "failed"
			uuid_cache.Set(idempotencyKey)
			c.Status(http.StatusInternalServerError)
			c.Error(transferErr)
			return
		}

		idempotencyKey.Status = "completed"
		uuid_cache.Set(idempotencyKey)
		c.IndentedJSON(http.StatusOK, res)

		// print("wtf...")
		// c.Status(http.StatusInternalServerError)
		// c.Error(idempotencyErr)
	})

	

	return &app
}

func (this *Application) Run() {
	// this.routerApp.Run()
	defer this.db.Close()
	defer this.uuid_cache.Close()

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


