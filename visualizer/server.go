package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	// 命令行参数
	port := flag.Int("port", 8081, "Port for the visualizer backend API")
	stateFile := flag.String("file", "../deep_compiler.json", "Path to the LLMVM state JSON file")
	flag.Parse()

	log.Printf("Starting Visualizer Backend on port %d...", *port)
	log.Printf("Monitoring state file: %s", *stateFile)

	// 提供数据的核心 API
	http.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		// 允许跨域（本地开发方便）
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// 读取文件
		absPath, _ := filepath.Abs(*stateFile)
		data, err := ioutil.ReadFile(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, fmt.Sprintf("State file not found at %s. Ensure LLMVM is running and saving to this path.", absPath), http.StatusNotFound)
			} else {
				http.Error(w, fmt.Sprintf("Error reading state file: %v", err), http.StatusInternalServerError)
			}
			return
		}

		// 为了防止 JSON 不完整（写入过程中正好被读取），这里做一次基础的校验
		var testMap map[string]interface{}
		if err := json.Unmarshal(data, &testMap); err != nil {
			http.Error(w, fmt.Sprintf("State file is currently being written or is malformed: %v", err), http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Fatal(http.ListenAndServe(addr, nil))
}
