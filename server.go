// main HTTP server component
package main

import (
	"encoding/json"
	"github.com/dchest/uniuri"
	"github.com/fvbock/endless"
	"github.com/gin-gonic/gin"
	"github.com/mediocregopher/radix.v2/pool"
	"github.com/mediocregopher/radix.v2/redis"
	"github.com/op/go-logging"
	"net/http"
)

const (
	listenPort              = ":8080"
	mnemoLen                = 10
	defaultLifetime         = 60 * 60 * 24
	maxMnemoFindTries       = 10
	secretsStoredCounter    = "KeeDropStoredKeysCounter"
	secretsRetrievedCounter = "KeeDropRetrievedKeysCounter"
)

var logger = logging.MustGetLogger("keedrop")

// structure to store the secret in Redis
// only the secret key remains with the sender
// secret for test.json: Lz5DP4grKMN9efoL9dt!S81X7AFGhin3OHDgbB8qcqQ=
type secretData struct {
	PubKey string `json:"pubkey" binding:"required"`
	Nonce  string `json:"nonce" binding:"required"`
	Secret string `json:"secret" binding:"required"`
}

func increaseCounter(redis *redis.Client, counterName string) {
	if _, err := redis.Cmd("INCR", counterName).Int64(); err != nil {
		logger.Error("Could not increase counter", err)
	}
}

// stores the secret in Redis and returns the key(mnemo) where it can be found
func saveInRedis(redis *pool.Pool, data *secretData) (string, bool) {
	conn, err := redis.Get()
	if err != nil {
		logger.Error("Could not connect to Redis")
		return "", false
	}
	defer redis.Put(conn)

	jsonData, jsonErr := json.Marshal(data)
	if jsonErr != nil {
		logger.Error("Could not marshal secret to JSON.", jsonErr)
		return "", false
	}
	for i := 0; i < maxMnemoFindTries; i++ {
		mnemo := uniuri.NewLen(mnemoLen)
		if _, err := conn.Cmd("SET", mnemo, jsonData, "NX", "EX", defaultLifetime).Str(); err == nil {
			increaseCounter(conn, secretsStoredCounter)
			return mnemo, true
		} else {
			logger.Error("Could not write secret, probably key collision.", err)
		}
	}
	logger.Error("Could not find unused mnemo after", maxMnemoFindTries, "tries")
	return "", false
}

// retrieves the secret from Redis, deleting it at the same time
func loadFromRedis(redis *pool.Pool, mnemo string) (*secretData, bool) {
	conn, err := redis.Get()
	if err != nil {
		logger.Error("Could not connect to Redis.", err)
		return nil, false
	}
	defer redis.Put(conn)

	conn.PipeAppend("MULTI")
	conn.PipeAppend("GET", mnemo)
	conn.PipeAppend("DEL", mnemo)
	conn.PipeAppend("EXEC")

	// the first 3 commands should only contain "OK" and "QUEUED", no real data
	for i := 0; i < 3; i++ {
		if err := conn.PipeResp().Err; err != nil {
			logger.Error("Redis error.", err)
			return nil, false
		}
	}
	if results, err := conn.PipeResp().Array(); err == nil {
		// array contains the results after MULTI in order
		encodedData, _ := results[0].Bytes()
		if len(encodedData) == 0 { // it means the secret wasn't found
			return nil, true
		} else {
			secret := new(secretData)
			if err := json.Unmarshal(encodedData, secret); err == nil {
				increaseCounter(conn, secretsRetrievedCounter)
				return secret, true
			} else {
				logger.Error("Could not unmarshal JSON data: ", encodedData)
				return nil, false
			}
		}
	} else {
		logger.Error("Error executing batch.", err)
		return nil, false
	}
}

// the Gin handlers all want a Redis connection, too
type redisUsingGinHandler func(*pool.Pool, *gin.Context)

// POST /api/secret
func storeSecret(redis *pool.Pool, ctx *gin.Context) {
	var secret secretData
	if ctx.BindJSON(&secret) == nil {
		if mnemo, ok := saveInRedis(redis, &secret); ok {
			ctx.JSON(http.StatusOK, gin.H{"mnemo": mnemo})
		} else {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Could not store secret"})
		}
	} else {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "bad JSON data"})
	}
}

// GET /api/secret/:mnemo
func retrieveSecret(redis *pool.Pool, ctx *gin.Context) {
	mnemo := ctx.Param("mnemo")
	logger.Debug("Reading data for mnemo:", mnemo)
	if secret, ok := loadFromRedis(redis, mnemo); !ok {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Could not read secret"})
	} else {
		if secret == nil {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "No such secret"})
		} else {
			ctx.JSON(http.StatusOK, secret)
		}
	}
}

// ensures that the Gin handler function receives a Redis connection, too
func wrapHandler(redis *pool.Pool, wrapped redisUsingGinHandler) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		wrapped(redis, ctx)
	}
}

// application entry point
func main() {
	redis, err := pool.New("tcp", "localhost:6379", 10)
	if err != nil {
		logger.Fatal("Cannot connect to Redis")
	}
	router := gin.Default()

	router.POST("/api/secret", wrapHandler(redis, storeSecret))
	router.GET("/api/secret/:mnemo", wrapHandler(redis, retrieveSecret))
	router.Static("/assets", "./assets")
	router.StaticFile("/r", "./retrieve.html")
	router.StaticFile("/", "./store.html")
	endless.ListenAndServe(listenPort, router)
}
