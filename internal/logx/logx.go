package logx

import (
	"encoding/json"
	"log"
	"time"
)

func logKV(level, event string, kv ...any) {
	fields := map[string]any{
		"ts":    time.Now().Format(time.RFC3339Nano),
		"level": level,
		"event": event,
	}

	for i := 0; i+1 < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok || k == "" {
			continue
		}
		fields[k] = kv[i+1]
	}

	line, err := json.Marshal(fields)
	if err != nil {
		log.Printf("[logx] marshal failed: %v", err)
		return
	}
	log.Print(string(line))
}

func Info(event string, kv ...any) {
	logKV("info", event, kv...)
}

func Warn(event string, kv ...any) {
	logKV("warn", event, kv...)
}

func Error(event string, kv ...any) {
	logKV("error", event, kv...)
}
