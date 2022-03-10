package main

import (
	"fmt"
	"github.com/allegro/bigcache/v3"
	"github.com/gin-gonic/gin"
	"github.com/syndtr/goleveldb/leveldb"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"flag"
)

var (
	LevelDB *leveldb.DB
	Cache   *bigcache.BigCache
)

func init() {
	Cache, _ = bigcache.NewBigCache(bigcache.DefaultConfig(10 * time.Minute))
	LevelDB, _ = leveldb.OpenFile("leveldb", nil)
}

func main() {
	addr := flag.String("addr", "0.0.0.0:5999", "server address")
	mode := flag.String("mode", "release", "gin mode")
	flag.Parse()

	if *mode == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	defer func() {
		_ = Cache.Close()
		_ = LevelDB.Close()
	}()

	r := gin.Default()

	r.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})
	r.POST("/upload", UploadHandler)
	r.GET("/download/:filename", DownloadHandler)
	r.POST("/delete/:filename", DeleteHandler)
	r.GET("/all", func(c *gin.Context) {
		snapshot, _ := LevelDB.GetSnapshot()
		defer snapshot.Release()
		iter := snapshot.NewIterator(nil, nil)
		defer iter.Release()
		files := make([]string, 0)
		for iter.Next() {
			key := iter.Key()
			files = append(files, string(key))
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "files": files})
	})

	log.Fatal(r.Run(*addr))
}

func UploadHandler(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "error": "param error"})
		return
	}

	if fileHeader.Size > 1024*1024*10 {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "error": "file size exceeds 10MB"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "error": err.Error()})
		return
	}
	defer file.Close()

	data, _ := ioutil.ReadAll(file)
	filename := fileHeader.Filename

	exist, _ := LevelDB.Has([]byte(filename), nil)
	if exist {
		c.JSON(http.StatusOK, gin.H{"code": -1, "error": "filename already exist"})
		return
	}

	_ = LevelDB.Put([]byte(filename), data, nil)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

func DeleteHandler(c *gin.Context) {
	filename := c.Param("filename")
	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "error": "filename is null"})
		return
	}

	_ = Cache.Delete(filename)
	_ = LevelDB.Delete([]byte(filename), nil)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

func DownloadHandler(c *gin.Context) {
	filename := c.Param("filename")
	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "error": "filename is null"})
		return
	}

	data, err := Cache.Get(filename)
	if err != nil {
		if err != bigcache.ErrEntryNotFound {
			c.JSON(http.StatusInternalServerError, gin.H{"code": -2, "error": "cache error"})
			return
		}

		data, err = LevelDB.Get([]byte(filename), nil)
		if err != nil {
			if err != leveldb.ErrNotFound {
				c.JSON(http.StatusInternalServerError, gin.H{"code": -3, "error": "db error"})
			} else {
				c.JSON(http.StatusOK, gin.H{"code": 1, "message": "not found"})
			}
			return
		}

		_ = Cache.Set(filename, data)
	}

	delivery(c, filename, data)
}

func delivery(c *gin.Context, filename string, content []byte) {
	contentType := ""
	s := strings.Split(filename, ".")
	fileType := s[len(s)-1]

	switch fileType {
	case "jpg":
		contentType = "image/jpeg"
	case "png":
		contentType = "image/png"
	case "img":
		contentType = "application/x-img"
	case "jpe", "jpeg":
		contentType = "image/jpeg"
	case "gif":
		contentType = "image/gif"
	case "txt":
		contentType = "text/plain"
	case "zip":
		contentType = "application/zip"
	case "pbf":
		contentType = "application/pdf"
	case "word":
		contentType = "application/msword"
	default:
		contentType = "application/octet-stream"
	}

	c.Writer.WriteHeader(http.StatusOK)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, url.QueryEscape(filename)))
	c.Header("Content-Type", contentType)
	c.Header("Accept-Length", fmt.Sprintf("%d", len(content)))
	c.Header("Filename", filename)
	_, _ = c.Writer.Write(content)
}
