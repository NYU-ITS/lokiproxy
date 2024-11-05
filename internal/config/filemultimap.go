package config

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

func load(filename string) (map[string]map[string]interface{}, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data := make(map[string]map[string]interface{})

	currentKey := ""
	currentValues := make(map[string]interface{})
	lineNum := 0
	fileScanner := bufio.NewScanner(file)
	fileScanner.Split(bufio.ScanLines)
	for fileScanner.Scan() {
		lineNum += 1
		line := fileScanner.Text()
		trimmed := strings.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		} else if trimmed[0] == '#' { // comment
			continue
		} else if line[0] != ' ' { // key
			if currentKey != "" {
				data[currentKey] = currentValues
				currentValues = make(map[string]interface{})
			}
			currentKey = trimmed
		} else { // value
			if currentKey == "" {
				return nil, fmt.Errorf("value before any key line %d", lineNum)
			}
			currentValues[trimmed] = nil
		}
	}
	if currentKey != "" {
		data[currentKey] = currentValues
	}

	return data, nil
}

type FileMultiMap struct {
	data atomic.Pointer[map[string]map[string]interface{}]
}

func NewFileMultiMap(filename string, ctxCancel context.CancelFunc) (*FileMultiMap, error) {
	// Load file now
	log.Printf("loading %s", filename)
	data, err := load(filename)
	if err != nil {
		ctxCancel()
		return nil, fmt.Errorf("error loading file: %e", err)
	}

	// Create object
	res := &FileMultiMap{}
	res.data.Store(&data)

	// Reload file automatically
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			data, err := load(filename)
			if err != nil {
				ctxCancel()
				log.Printf("error loading file: %s", err)
				return
			}
			res.data.Store(&data)
		}
	}()

	return res, nil
}

func (m *FileMultiMap) Get(key string) (map[string]interface{}, bool) {
	res, ok := (*m.data.Load())[key]
	return res, ok
}
