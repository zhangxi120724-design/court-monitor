package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type directZoneType struct {
	ID    int                   `json:"Id"`
	Name  string                `json:"Name"`
	Zones map[string]directZone `json:"Zones"`
}

type directZone struct {
	ID   int    `json:"Id"`
	Name string `json:"Name"`
}

type directSchedule struct {
	CalendarData []directScheduleHour `json:"CalendarData"`
}

type directScheduleHour struct {
	ClassesPerDay [][]directScheduleSlot `json:"ClassesPerDay"`
}

type directScheduleSlot struct {
	BookingDuration string   `json:"BookingDuration"`
	Durations       []string `json:"Durations"`
	Status          string   `json:"Status"`
	StartTime       string   `json:"StartTime"`
	EndTime         string   `json:"EndTime"`
}

type directAPIClient struct {
	baseURL string
	token   string
	userID  int
	client  *http.Client
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("Court Monitor started")

	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	telegramChatID := os.Getenv("TELEGRAM_CHAT_ID")

	if telegramToken == "" || telegramChatID == "" {
		log.Println("Missing TELEGRAM_BOT_TOKEN or TELEGRAM_CHAT_ID")
		return
	}

	now := time.Now().In(time.FixedZone("SGT", 8*3600))
	hour := now.Hour()
	weekday := now.Weekday()

	isWeekday := weekday >= time.Monday && weekday <= time.Friday
	isWeekend := weekday == time.Saturday || weekday == time.Sunday

	inWindow := false
	if isWeekday && hour >= 15 && hour < 23 {
		inWindow = true
	}
	if isWeekend && hour >= 15 && hour < 23 {
		inWindow = true
	}

	log.Printf("Time: %s, Weekday: %v, Weekend: %v, Hour: %d, InWindow: %v",
		now.Format("15:04"), isWeekday, isWeekend, hour, inWindow)

	if !inWindow {
		log.Println("Outside target time window")
		return
	}

	// 初始化 API 客户端
	baseURL := os.Getenv("PERFECTGYM_BASE_URL")
	if baseURL == "" {
		log.Println("Missing PERFECTGYM_BASE_URL")
		sendTelegramNotification(telegramToken, telegramChatID, "❌ 配置错误: 缺少 PERFECTGYM_BASE_URL")
		return
	}

	storagePath := ".auth/storage-state.json"
	api, err := newDirectAPIClient(baseURL, storagePath)
	if err != nil {
		log.Printf("❌ Failed to init API client: %v", err)
		sendTelegramNotification(telegramToken, telegramChatID, fmt.Sprintf("❌ 认证失败: %v", err))
		return
	}

	// 获取场地类型
	zoneTypes, err := api.getClubZoneTypes()
	if err != nil {
		log.Printf("Failed to get zone types: %v", err)
		sendTelegramNotification(telegramToken, telegramChatID, fmt.Sprintf("❌ 获取场地类型失败: %v", err))
		return
	}

	// 检查 Indoor (85) 和 Outdoor (83)
	var availableSlots []string

	// 检查 Indoor
	if zoneType, ok := zoneTypes["85"]; ok {
		slots, err := checkZoneAvailability(api, 85, zoneType, "Indoor", now)
		if err != nil {
			log.Printf("Check indoor failed: %v", err)
		} else {
			availableSlots = append(availableSlots, slots...)
		}
	}

	// 检查 Outdoor
	if zoneType, ok := zoneTypes["83"]; ok {
		slots, err := checkZoneAvailability(api, 83, zoneType, "Outdoor", now)
		if err != nil {
			log.Printf("Check outdoor failed: %v", err)
		} else {
			availableSlots = append(availableSlots, slots...)
		}
	}

	// 发送消息
	msg := fmt.Sprintf("🎾 Kallang Tennis Hub 场地监听\n\n时间: %s\n\n", now.Format("15:04"))

	if len(availableSlots) > 0 {
		msg += "✅ 发现可用场地:\n\n"
		for _, slot := range availableSlots {
			msg += fmt.Sprintf("• %s\n", slot)
		}
	} else {
		msg += "暂无可用场地\n"
		msg += "监听中...\n"
	}

	sendTelegramNotification(telegramToken, telegramChatID, msg)
}

func checkZoneAvailability(api *directAPIClient, zoneTypeID int, zoneType directZoneType, zoneTypeName string, now time.Time) ([]string, error) {
	var slots []string
	scheduleDate := now.Format("2006-01-02")

	for _, zone := range zoneType.Zones {
		schedule, err := api.getWeeklySchedule(zoneTypeID, zone.ID, scheduleDate)
		if err != nil {
			log.Printf("Get schedule for %s %s: %v", zoneTypeName, zone.Name, err)
			continue
		}

		for _, hour := range schedule.CalendarData {
			for _, daySlots := range hour.ClassesPerDay {
				for _, slot := range daySlots {
					if strings.EqualFold(slot.Status, "Bookable") {
						// 获取时间戳
						slotHour, slotMin := parseTimeString(slot.StartTime)
						duration := slot.BookingDuration
						slots = append(slots, fmt.Sprintf("%s %s - %02d:%02d (%s)", zoneTypeName, zone.Name, slotHour, slotMin, duration))
					}
				}
			}
		}
	}

	return slots, nil
}

func parseTimeString(timeStr string) (int, int) {
	parts := strings.Split(timeStr, ":")
	hour := 0
	minute := 0
	if len(parts) > 0 {
		h, _ := strconv.Atoi(parts[0])
		hour = h
	}
	if len(parts) > 1 {
		m, _ := strconv.Atoi(parts[1])
		minute = m
	}
	return hour, minute
}

func newDirectAPIClient(baseURL, storagePath string) (*directAPIClient, error) {
	// 获取 clientportal2 base URL
	index := strings.Index(strings.ToLower(baseURL), "/clientportal2")
	if index < 0 {
		return nil, fmt.Errorf("invalid base URL format")
	}
	clientPortalBase := baseURL[:index] + "/clientportal2"

	// 从 storage 文件读取 token
	token, err := loadAuthToken(storagePath)
	if err != nil {
		return nil, err
	}

	userID, err := userIDFromAuthToken(token)
	if err != nil {
		return nil, err
	}

	return &directAPIClient{
		baseURL: clientPortalBase,
		token:   token,
		userID:  userID,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}, nil
}

func (api *directAPIClient) getClubZoneTypes() (map[string]directZoneType, error) {
	var zoneTypes map[string]directZoneType
	if err := api.postJSON("/FacilityBookings/FacilityCalendar/GetClubZoneTypes", map[string]int{
		"clubId": 1,
	}, &zoneTypes); err != nil {
		return nil, fmt.Errorf("get club zone types: %w", err)
	}
	return zoneTypes, nil
}

func (api *directAPIClient) getWeeklySchedule(zoneTypeID, zoneID int, scheduleDate string) (*directSchedule, error) {
	payload := map[string]interface{}{
		"clubId":     1,
		"date":       scheduleDate,
		"zoneTypeId": strconv.Itoa(zoneTypeID),
		"zoneId":     zoneID,
		"daysInWeek": 6,
	}

	var schedule directSchedule
	if err := api.postJSON("/FacilityBookings/FacilityCalendar/GetWeeklySchedule", payload, &schedule); err != nil {
		return nil, err
	}
	return &schedule, nil
}

func (api *directAPIClient) postJSON(path string, payload interface{}, out interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := api.baseURL + path
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("authorization", "Bearer "+api.token)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")

	resp, err := api.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("api returned %d: %s", resp.StatusCode, string(respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func loadAuthToken(storagePath string) (string, error) {
	data, err := os.ReadFile(storagePath)
	if err != nil {
		return "", fmt.Errorf("read auth storage: %w", err)
	}

	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		return "", fmt.Errorf("decode auth storage: %w", err)
	}

	// 尝试从 origins 中获取 token
	if origins, ok := state["origins"].([]interface{}); ok {
		for _, origin := range origins {
			if originMap, ok := origin.(map[string]interface{}); ok {
				if localStorage, ok := originMap["localStorage"].([]interface{}); ok {
					for _, item := range localStorage {
						if itemMap, ok := item.(map[string]interface{}); ok {
							if name, ok := itemMap["name"].(string); ok && name == "CpAuthToken" {
								if value, ok := itemMap["value"].(string); ok && value != "" {
									return value, nil
								}
							}
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("CpAuthToken not found in storage")
}

func userIDFromAuthToken(token string) (int, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid JWT format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, fmt.Errorf("decode token payload: %w", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0, fmt.Errorf("decode token claims: %w", err)
	}

	if value, ok := claims["UserId"]; ok {
		if userID, ok := value.(float64); ok {
			return int(userID), nil
		}
	}

	return 0, fmt.Errorf("UserId not found in token")
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

	if resp.StatusCode == 200 {
		log.Println("Telegram notification sent successfully")
	} else {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Telegram error: %s", string(body))
	}
}
