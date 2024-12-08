package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
)

const (
	serverAddress = "127.0.0.1:9000"
	maxPacketSize = 1400
)

type Request struct {
	Operation int               `json:"operation"`
	Options   map[string]string `json:"options"`
	Filename  string            `json:"filename"`
}

func main() {
	conn, err := net.Dial("tcp", serverAddress)
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	// 入力を取得
	filePath := getInput("Enter the path of the video file to upload: ")
	operation := selectOperation()
	options := make(map[string]string)

	// オプションを取得
	switch operation {
	case 2: // resolution
		options["resolution"] = getInput("Enter the resolution (e.g., 1280x720): ")
	case 3: // aspect ratio
		options["aspect_ratio"] = getInput("Enter the aspect ratio (e.g., 16:9): ")
	case 5,6: // gif or webm
		options["start_time"] = getInput("Enter the start time (e.g., 00:00:00): ")
		options["duration"] = getInput("Enter the duration (in seconds): ")
	}

	filename := filepath.Base(filePath)
	request := Request{Operation: operation, Options: options, Filename: filename}
	requestJSON, _ := json.Marshal(request)
	jsonSize := len(requestJSON)

	// ファイルを開く
	file, err := os.Open(filePath)
	if err != nil {
		log.Fatalf("Failed to open file: %v", err)
	}
	defer file.Close()

	// ファイルサイズを取得
	fileInfo, err := file.Stat()
	if err != nil {
		log.Fatalf("Failed to get file info: %v", err)
	}
	payloadSize := fileInfo.Size()

	if payloadSize > 1<<32 {
		log.Fatalf("File size exceeds 4GB limit")
	}

	// ヘッダーを作成
	header := createHeader(jsonSize, int(payloadSize))
	conn.Write(header)
	conn.Write(requestJSON)

	// ファイルデータを送信
	buffer := make([]byte, maxPacketSize)
	for {
		n, err := file.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("Error reading file: %v", err)
		}
		conn.Write(buffer[:n])
	}
	log.Println("File uploaded successfully.")

	// サーバーからレスポンスを受信
	responseHeader := make([]byte, 8)
	if _, err := conn.Read(responseHeader); err != nil {
		log.Fatalf("Failed to read response header: %v", err)
	}
	responseJSONSize := int(binary.BigEndian.Uint16(responseHeader[:2]))
	responsePayloadSize := int(binary.BigEndian.Uint32(responseHeader[3:]))
	responseJSON := make([]byte, responseJSONSize)
	n, err := conn.Read(responseJSON)
	if err != nil {
		log.Fatalf("Failed to read response JSON: %v", err)
	}
	log.Printf("Received raw JSON: %s", string(responseJSON[:n]))

	// レスポンスを解析
	var response map[string]string
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		log.Fatalf("Failed to parse response JSON: %v", err)
	}
	log.Printf("Server response: %s", response)

	if response["status"] != "success" {
		log.Fatalf("File processing failed: %s", response["message"])
	}

	// 処理済みファイルをダウンロード
	if responsePayloadSize > 0 {
		downloadDir := "downloads"
		if err := os.MkdirAll(downloadDir, os.ModePerm); err != nil {
			log.Fatalf("Failed to create downloads directory: %v", err)
		}

		processedFilename := response["filename"]
		outputFilePath := filepath.Join(downloadDir, processedFilename)

		responseFile, err := os.Create(outputFilePath)
		if err != nil {
			log.Fatalf("Failed to create output file: %v", err)
		}
		defer responseFile.Close()

		receivedBytes := 0
		for receivedBytes < responsePayloadSize {
			n, err := conn.Read(buffer)
			if err != nil && err != io.EOF {
				log.Fatalf("Failed to read file data: %v", err)
			}
			responseFile.Write(buffer[:n])
			receivedBytes += n
		}
		log.Printf("Processed file saved as '%s'", outputFilePath)
	}
}

func createHeader(jsonSize, payloadSize int) []byte {
	header := make([]byte, 8)
	binary.BigEndian.PutUint16(header[:2], uint16(jsonSize))
	binary.BigEndian.PutUint32(header[3:], uint32(payloadSize))
	return header
}

func getInput(prompt string) string {
	fmt.Print(prompt)
	var input string
	fmt.Scanln(&input)
	return input
}

func selectOperation() int {
	fmt.Println("Please enter a number from 1 to 6:")
	fmt.Println("1 : Compress the video file")
	fmt.Println("2 : Change the resolution of the video")
	fmt.Println("3 : Change the aspect ratio of the video")
	fmt.Println("4 : Extract audio from the video")
	fmt.Println("5 : Create a GIF from a time range")
	fmt.Println("6 : Convert the video to WebM format")
	var choice int
	for {
		fmt.Print("Enter your choice: ")
		_, err := fmt.Scanln(&choice)
		if err != nil || choice < 1 || choice > 6 {
			fmt.Println("Invalid choice. Please enter a number between 1 and 5.")
		} else {
			break
		}
	}
	return choice
}
