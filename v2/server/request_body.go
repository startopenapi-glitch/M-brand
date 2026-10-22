package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// 读取前设上限；未知 Content-Length 的分块请求也最多读取 max+1 字节。
func readJSONBodyLimit(c *gin.Context, dest any, max int64) bool {
	if c.Request.ContentLength > max {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "请求数据过大"})
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
	body, err := io.ReadAll(c.Request.Body)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "请求数据过大"})
		return false
	}
	if err != nil || json.Unmarshal(body, dest) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求数据无效"})
		return false
	}
	return true
}
