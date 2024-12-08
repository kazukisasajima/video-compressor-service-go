package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	serverAddress = "0.0.0.0:9000"
	maxPacketSize = 1400
	uploadDir     = "uploads"
)

// クライアントからのリクエストを表現する構造体
type Request struct {
	Operation int               `json:"operation"`
	Options   map[string]string `json:"options"`
	Filename  string            `json:"filename"`
}

var operationMap = map[int]string{
	1: "compress",
	2: "resolution",
	3: "aspect_ratio",
	4: "audio",
	5: "gif",
	6: "webm",
}

func main() {
	// アップロードディレクトリの作成
	err := os.MkdirAll(uploadDir, os.ModePerm)
	if err != nil {
		log.Fatalf("Failed to create upload directory: %v", err)
	}

	// サーバーのリスナーを開始
	listener, err := net.Listen("tcp", serverAddress)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	defer listener.Close()

	log.Println("Server is running and waiting for connections...")

	// クライアントからの接続を待機
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

// クライアントからの接続を処理する関数
func handleConnection(conn net.Conn) {
	defer conn.Close()

	// ヘッダー部分（8バイト）の読み取り
	header := make([]byte, 8)
	if _, err := conn.Read(header); err != nil {
		log.Printf("Failed to read header: %v", err)
		return
	}

	// ヘッダー情報をパース
	jsonSize := int(binary.BigEndian.Uint16(header[:2]))
	payloadSize := int(binary.BigEndian.Uint32(header[3:]))
	log.Printf("Header received: JSON size=%d, Payload size=%d", jsonSize, payloadSize)

	// JSONメタデータの読み取り
	jsonData := make([]byte, jsonSize)
	if _, err := conn.Read(jsonData); err != nil {
		log.Printf("Failed to read JSON metadata: %v", err)
		return
	}
	var request Request
	if err := json.Unmarshal(jsonData, &request); err != nil {
		log.Printf("Invalid JSON format: %v", err)
		return
	}
	log.Printf("Request received: %+v", request)

	// ファイル名がない場合のエラー処理
	if request.Filename == "" {
		sendError(conn, "Filename is missing in the request")
		return
	}

	// オペレーション番号が無効な場合のエラー処理
	operation, exists := operationMap[request.Operation]
	if !exists {
		sendError(conn, "Invalid operation number")
		return
	}

	// 保存するファイルパスを決定
	filePath := filepath.Join(uploadDir, request.Filename)

	// ファイルを保存
	file, err := os.Create(filePath)
	if err != nil {
		log.Printf("Failed to create file: %v", err)
		sendError(conn, "Failed to create file")
		return
	}
	defer file.Close()

	buffer := make([]byte, maxPacketSize)
	remaining := payloadSize
	for remaining > 0 {
		n, err := conn.Read(buffer)
		if err != nil {
			log.Printf("Error reading file: %v", err)
			sendError(conn, "Failed to receive file")
			return
		}
		file.Write(buffer[:n])
		remaining -= n
	}
	log.Printf("File saved at %s", filePath)

	// ファイルを処理
	outputPath, err := processFile(filePath, operation, request)
	if err != nil {
		log.Printf("Failed to process file: %v", err)
		sendError(conn, "Video processing failed")
		return
	}

	// 処理済みファイルをクライアントに送信
	sendFile(conn, outputPath)

	// 処理後のファイルとアップロードされたファイルを削除
	if err := os.Remove(outputPath); err != nil {
		log.Printf("Failed to delete processed file: %v", err)
	}
	if err := os.Remove(filePath); err != nil {
		log.Printf("Failed to delete uploaded file: %v", err)
	}
}

// ファイルの処理を行う関数
func processFile(filePath, operation string, request Request) (string, error) {
	var outputFilename string
	switch operation {
	case "compress":
		outputFilename = fmt.Sprintf("compressed_%s", request.Filename)
	case "resolution":
		outputFilename = fmt.Sprintf("resolution_%s", request.Filename)
	case "aspect_ratio":
		outputFilename = fmt.Sprintf("aspect_ratio_%s", request.Filename)
	case "audio":
		outputFilename = fmt.Sprintf("audio_%s.mp3", removeExtension(request.Filename))
	case "gif":
		outputFilename = fmt.Sprintf("gif_%s.gif", removeExtension(request.Filename))
	case "webm":
		outputFilename = fmt.Sprintf("webm_%s.webm", removeExtension(request.Filename))
	default:
		return "", fmt.Errorf("Invalid operation: %s", operation)
	}

	outputPath := filepath.Join(uploadDir, outputFilename)
	var cmd *exec.Cmd
	switch operation {
	case "compress":
		cmd = exec.Command("ffmpeg", "-i", filePath, "-vcodec", "libx264", "-crf", "28", outputPath)
	case "resolution":
		resolution := request.Options["resolution"]
		cmd = exec.Command("ffmpeg", "-i", filePath, "-vf", fmt.Sprintf("scale=%s", resolution), outputPath)
	case "aspect_ratio":
		aspectRatio := request.Options["aspect_ratio"]
		cmd = exec.Command("ffmpeg", "-i", filePath, "-vf", fmt.Sprintf("setdar=%s", aspectRatio), outputPath)
	// case "audio":
	// 	cmd = exec.Command("ffmpeg", "-i", filePath, "-q:a", "0", "-map", "a", outputPath)
	case "audio":
		// 音声ストリームがあるか確認
		audioCheckCmd := exec.Command("ffmpeg", "-i", filePath, "-map", "a", "-f", "null", "-")
		if err := audioCheckCmd.Run(); err != nil {
			return "", fmt.Errorf("no audio stream found in the file")
		}
		// 音声を抽出
		outputFilename = fmt.Sprintf("audio_%s.mp3", removeExtension(request.Filename))
		cmd = exec.Command("ffmpeg", "-i", filePath, "-q:a", "0", "-map", "a", outputPath)	
	case "gif":
		startTime := request.Options["start_time"]
		duration := request.Options["duration"]
		cmd = exec.Command("ffmpeg", "-i", filePath, "-ss", startTime, "-t", duration, "-vf", "fps=10,scale=320:-1", outputPath)
	case "webm":
		startTime := request.Options["start_time"]
		duration := request.Options["duration"]
		cmd = exec.Command("ffmpeg", "-i", filePath, "-ss", startTime, "-t", duration, "-c:v", "libvpx-vp9", "-b:v", "1M", "-c:a", "libopus", outputPath)
	default:
		return "", fmt.Errorf("Invalid operation: %s", operation)
	}

	if err := cmd.Run(); err != nil {
		return "", err
	}
	log.Printf("File processed successfully and saved at %s", outputPath)
	return outputPath, nil
}

// ファイル拡張子を除去する関数
func removeExtension(filename string) string {
	return filename[:len(filename)-len(filepath.Ext(filename))]
}

// エラーをクライアントに送信する関数
func sendError(conn net.Conn, errorMessage string) {
	response := map[string]string{"status": "error", "message": errorMessage}
	responseJSON, _ := json.Marshal(response)
	conn.Write(responseJSON)
}

// 処理済みファイルをクライアントに送信する関数
func sendFile(conn net.Conn, filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		sendError(conn, "Failed to open processed file")
		return
	}
	defer file.Close()

	response := map[string]string{
		"status":   "success",
		"filename": filepath.Base(filePath),
	}
	responseJSON, _ := json.Marshal(response)
	log.Printf("Sending response JSON: %s", string(responseJSON))

	responseHeader := make([]byte, 8)
	binary.BigEndian.PutUint16(responseHeader[:2], uint16(len(responseJSON)))
	fileInfo, _ := file.Stat()
	binary.BigEndian.PutUint32(responseHeader[3:], uint32(fileInfo.Size()))

	conn.Write(responseHeader)
	conn.Write(responseJSON)

	buffer := make([]byte, maxPacketSize)
	for {
		n, err := file.Read(buffer)
		if n > 0 {
			conn.Write(buffer[:n])
		}
		if err != nil {
			break
		}
	}
	log.Printf("File sent to client: %s", filePath)
}
