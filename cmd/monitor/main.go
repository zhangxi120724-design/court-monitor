package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("Court Monitor started")

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	
	if token == "" || chatID == "" {
		log.Println("Missing TELEGRAM_BOT_TOKEN or TELEGRAM_CHAT_ID")
		return
	}

	now := time.Now().In(time.FixedZone("SGT", 8*3600))
	hour := now.Hour()
	weekday := now.Weekday()

	isWeekday := weekday >= time.Monday && weekday <= time.Friday
	isWeekend := weekday == time.Saturday || weekday == time.Sunday

	inWindow := false
	if isWeekday && hour >= 20 && hour < 22 {
		inWindow = true
	}
	if isWeekend && hour >= 17 && hour < 22 {
		inWindow = true
	}

	log.Printf("Time: %s, Weekday: %v, Weekend: %v, Hour: %d, InWindow: %v", 
		now.Format("15:04"), isWeekday, isWeekend, hour, inWindow)

	if inWindow {
		msg := fmt.Sprintf("🎾 Monitor running at %s\n\nTime window is active. Would check court availability.", 
			now.Format("15:04"))
		sendTelegramNotification(token, chatID, msg)
	}
}

func sendTelegramNotification(token, chatID, text string) {
	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)

	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("Failed to send Telegram: %v", err)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 {
		log.Println("Telegram notification sent successfully")
	} else {
		log.Printf("Telegram error: %s", string(respBody))
	}
}
