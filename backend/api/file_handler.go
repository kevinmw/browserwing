package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/browserwing/browserwing/pkg/downloader"
	"github.com/browserwing/browserwing/pkg/logger"
	"github.com/gin-gonic/gin"
)

type DownloadImagesRequest struct {
	Images []struct {
		Index int    `json:"index"`
		Src    string `json:"src"`
		Alt    string `json:"alt"`
	} `json:"images"`
	Directory string `json:"directory"` // 可选，默认 "./downloads/images"
}

type DownloadImagesResponse struct {
	Success bool     `json:"success"`
	Images  []string `json:"images"` // 本地文件路径列表
	Message string   `json:"message"`
}

// DownloadImages 批量下载图片
func (h *Handler) DownloadImages(c *gin.Context) {
	var req DownloadImagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 创建下载目录
	downloadDir := req.Directory
	if downloadDir == "" {
		downloadDir = "./downloads/images"
	}
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create directory: %v", err)})
		return
	}

	// 使用现有的下载器
	imgDownloader := downloader.NewImageDownloader(downloadDir)

	downloadedPaths := []string{}
	failedImages := []string{}

	for _, img := range req.Images {
		if img.Src == "" {
			continue
		}

		localPath, err := imgDownloader.DownloadImage(img.Src)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to download image %s: %v", img.Src, err)})
			failedImages = append(failedImages, img.Src)
			continue
		}

		downloadedPaths = append(downloadedPaths, localPath)
	}

	response := DownloadImagesResponse{
		Success: len(failedImages) == 0,
		Images:  downloadedPaths,
		Message: fmt.Sprintf("Downloaded %d/%d images", len(downloadedPaths), len(req.Images)),
	}

	if len(failedImages) > 0 {
		response.Message += fmt.Sprintf(", Failed: %d", len(failedImages))
	}

	c.JSON(http.StatusOK, response)
}

type SaveMarkdownRequest struct {
	Content   string `json:"content"`
	Filename  string `json:"filename"`
	Directory string `json:"directory"` // 可选，默认 "./markdown"
}

type SaveMarkdownResponse struct {
	Success bool   `json:"success"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// SaveMarkdown 保存markdown文件
func (h *Handler) SaveMarkdown(c *gin.Context) {
	var req SaveMarkdownRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 创建目录
	saveDir := req.Directory
	if saveDir == "" {
		saveDir = "./markdown"
	}
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		logger.Error(c, "Failed to create markdown directory: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create directory: %v", err)})
		return
	}

	// 确保文件名以.md结尾
	filename := req.Filename
	if !strings.HasSuffix(filename, ".md") {
		filename += ".md"
	}

	// 保存文件
	filePath := filepath.Join(saveDir, filename)
	if err := os.WriteFile(filePath, []byte(req.Content), 0644); err != nil {
		logger.Error(c, "Failed to save markdown file: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save file: %v", err)})
		return
	}

	logger.Info(c, "Markdown saved to: %s", filePath)

	c.JSON(http.StatusOK, SaveMarkdownResponse{
		Success: true,
		Path:    filePath,
		Message: "Markdown file saved successfully",
	})
}
