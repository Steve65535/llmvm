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
	port := flag.Int("port", 8081, "Port for the visualizer backend API")
	stateFile := flag.String("file", "../deep_compiler.json", "Path to the LLMVM state JSON file")
	flag.Parse()

	log.Printf("Starting Visualizer Backend on port %d...", *port)
	log.Printf("Monitoring state file: %s", *stateFile)

	// 读取并解析状态文件的辅助函数
	readState := func() (map[string]interface{}, error) {
		absPath, _ := filepath.Abs(*stateFile)
		data, err := ioutil.ReadFile(absPath)
		if err != nil {
			return nil, err
		}
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		return raw, nil
	}

	// /api/state — 返回任务树（兼容新旧格式）
	http.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		raw, err := readState()
		if err != nil {
			if os.IsNotExist(err) {
				absPath, _ := filepath.Abs(*stateFile)
				http.Error(w, fmt.Sprintf("State file not found at %s.", absPath), http.StatusNotFound)
			} else {
				http.Error(w, fmt.Sprintf("Error reading state file: %v", err), http.StatusInternalServerError)
			}
			return
		}

		// 新格式：{root: ..., artifacts: ...}，提取 root
		// 旧格式：顶层就是 TaskNode，直接返回
		var treeData interface{}
		if root, ok := raw["root"]; ok {
			treeData = root
		} else {
			treeData = raw
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(treeData)
	})

	// /api/artifacts — 返回 artifact store（新格式专用）
	http.HandleFunc("/api/artifacts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		raw, err := readState()
		if err != nil {
			http.Error(w, fmt.Sprintf("Error: %v", err), http.StatusInternalServerError)
			return
		}

		artifacts, ok := raw["artifacts"]
		if !ok {
			artifacts = map[string]interface{}{"artifacts": []interface{}{}, "counter": 0}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(artifacts)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Fatal(http.ListenAndServe(addr, nil))
}
