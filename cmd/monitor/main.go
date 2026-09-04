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
	PagerData    directPagerData      `json:"PagerData"`
}

type directScheduleHour struct {
	Hour          string                 `json:"Hour"`
	ClassesPerDay [][]directScheduleSlot `json:"ClassesPerDay"`
}

type directPagerData struct {
	Days []string `json:"Days"`
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

	log.Printf("Current time: %s", now.Format("15:04"))

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

	// 检查 Indoor (85) 和 Outdoor (83)
	var availableSlots []string

	// 检查 Indoor
	slots, err := checkZoneAvailability(api, 85, "Indoor", now)
	if err != nil {
		log.Printf("Check indoor failed: %v", err)
	} else {
		availableSlots = append(availableSlots, slots...)
	}

	// 检查 Outdoor
	slots, err = checkZoneAvailability(api, 83, "Outdoor", now)
	if err != nil {
		log.Printf("Check outdoor failed: %v", err)
	} else {
		availableSlots = append(availableSlots, slots...)
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

func checkZoneAvailability(api *directAPIClient, zoneTypeID int, zoneTypeName string, now time.Time) ([]string, error) {
	var slots []string

	scheduleDate := now.Format("2006-01-02")
	log.Printf("DEBUG: Checking %s (zoneTypeId=%d) starting from %s", zoneTypeName, zoneTypeID, scheduleDate)

	schedule, err := api.getWeeklySchedule(zoneTypeID, scheduleDate)
	if err != nil {
		return nil, fmt.Errorf("get schedule for %s: %w", zoneTypeName, err)
	}

	log.Printf("DEBUG: %s returned %d hours of data, days: %v", zoneTypeName, len(schedule.CalendarData), schedule.PagerData.Days)

	// 遍历所有小时
	for _, hourData := range schedule.CalendarData {
		// 遍历 7 天（每个小时有 7 天的数据）
		for dayIdx, daySlots := range hourData.ClassesPerDay {
			if dayIdx >= len(schedule.PagerData.Days) {
				break
			}

			currentDate := schedule.PagerData.Days[dayIdx]

			// 遍历这个时间段的所有 slot
			for _, slot := range daySlots {
				if strings.EqualFold(slot.Status, "Bookable") {
					// 从 slot.StartTime 直接提取时间 (格式: 2026-09-09T15:00:00)
					slotTime, err := time.Parse("2006-01-02T15:04:05", slot.StartTime)
					if err != nil {
						log.Printf("Failed to parse slot time %s: %v", slot.StartTime, err)
						continue
					}

					slotHour := slotTime.Hour()
					slotMin := slotTime.Minute()

					// 判断是否在目标时间窗口
					// TODO: 改回生产配置 (工作日 8-10pm, 周末 5-10pm)
					inTargetWindow := slotHour >= 15 && slotHour < 23 // 测试: 3pm-11pm

					if inTargetWindow {
						// 调用 Start API 获取具体的 Court 信息
						courts, errStart := api.getStartFacilityInfo(zoneTypeID, slot.StartTime)
						if errStart != nil {
							log.Printf("Failed to get facility info for %s at %s: %v", zoneTypeName, slot.StartTime, errStart)
							continue
						}

						// 对每个 court 添加通知
						for _, courtName := range courts {
							slots = append(slots, fmt.Sprintf("%s %s %s - %02d:%02d (%s)",
								currentDate, zoneTypeName, courtName, slotHour, slotMin, slot.BookingDuration))
						}
					}
				}
			}
		}
	}

	return slots, nil
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

func (api *directAPIClient) getWeeklySchedule(zoneTypeID int, scheduleDate string) (*directSchedule, error) {
	payload := map[string]interface{}{
		"clubId":     1,
		"date":       scheduleDate,
		"zoneTypeId": strconv.Itoa(zoneTypeID),
		"daysInWeek": 7,
	}

	var schedule directSchedule
	if err := api.postJSON("/FacilityBookings/FacilityCalendar/GetWeeklySchedule", payload, &schedule); err != nil {
		return nil, err
	}
	return &schedule, nil
}

func (api *directAPIClient) getStartFacilityInfo(zoneTypeID int, startDateTime string) ([]string, error) {
	// GET /FacilityBookings/BuyProductBeforeBookingFacility/Start
	url := fmt.Sprintf("%s/FacilityBookings/BuyProductBeforeBookingFacility/Start?clubId=1&zoneTypeId=%d&startDate=%s",
		api.baseURL, zoneTypeID, startDateTime)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("authorization", "Bearer "+api.token)
	req.Header.Set("accept", "application/json, text/plain, */*")
	req.Header.Set("cp-lang", "en")
	req.Header.Set("cp-mode", "desktop")
	req.Header.Set("x-requested-with", "XMLHttpRequest")

	resp, err := api.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("api returned %d: %s", resp.StatusCode, string(respBody))
	}

	// 解析响应获取 Court 名称
	var startResp map[string]interface{}
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return nil, fmt.Errorf("decode start response: %w", err)
	}

	var courtNames []string
	if data, ok := startResp["Data"].(map[string]interface{}); ok {
		if zones, ok := data["Zones"].([]interface{}); ok {
			for _, zone := range zones {
				if zoneMap, ok := zone.(map[string]interface{}); ok {
					if name, ok := zoneMap["Name"].(string); ok {
						courtNames = append(courtNames, name)
					}
				}
			}
		}
	}

	if len(courtNames) == 0 {
		return nil, fmt.Errorf("no courts found in start response")
	}

	return courtNames, nil
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

	// DEBUG: 打印 GetWeeklySchedule 的返回数据
	if path == "/FacilityBookings/FacilityCalendar/GetWeeklySchedule" {
		log.Printf("DEBUG GetWeeklySchedule response: %s", string(respBody))
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
		switch v := value.(type) {
		case float64:
			return int(v), nil
		case string:
			userID, err := strconv.Atoi(v)
			if err == nil {
				return userID, nil
			}
		}
		log.Printf("DEBUG: UserId found but wrong type: %T = %v", value, value)
	}

	log.Printf("DEBUG: Claims: %v", claims)
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
