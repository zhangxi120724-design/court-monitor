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
	"sort"
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

type slotInfo struct {
	date     string
	hour     int
	min      int
	zoneType string
	court    string
	duration string
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("Court Monitor started")

	now := time.Now().In(time.FixedZone("SGT", 8*3600))

	// 只在 09:30 - 21:00 之间运行
	hour := now.Hour()
	min := now.Minute()
	if hour < 9 || (hour == 9 && min < 30) || hour >= 21 {
		log.Printf("Outside monitoring window (09:30-21:00). Current time: %02d:%02d", hour, min)
		return
	}

	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	telegramChatID := os.Getenv("TELEGRAM_CHAT_ID")

	if telegramToken == "" || telegramChatID == "" {
		log.Println("Missing TELEGRAM_BOT_TOKEN or TELEGRAM_CHAT_ID")
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

	// 并行检查 Indoor (85) 和 Outdoor (83)
	type zoneResult struct {
		slots []slotInfo
		err   error
	}
	indoorCh := make(chan zoneResult, 1)
	outdoorCh := make(chan zoneResult, 1)

	go func() {
		slots, err := checkZoneAvailability(api, 85, "Indoor", now)
		indoorCh <- zoneResult{slots, err}
	}()

	go func() {
		slots, err := checkZoneAvailability(api, 83, "Outdoor", now)
		outdoorCh <- zoneResult{slots, err}
	}()

	var availableSlots []slotInfo
	if result := <-indoorCh; result.err == nil {
		availableSlots = append(availableSlots, result.slots...)
	}
	if result := <-outdoorCh; result.err == nil {
		availableSlots = append(availableSlots, result.slots...)
	}

	// 仅在发现场地时发送通知
	if len(availableSlots) > 0 {
		// 按日期+时间排序
		sort.Slice(availableSlots, func(i, j int) bool {
			if availableSlots[i].date != availableSlots[j].date {
				return availableSlots[i].date < availableSlots[j].date
			}
			if availableSlots[i].hour != availableSlots[j].hour {
				return availableSlots[i].hour < availableSlots[j].hour
			}
			return availableSlots[i].min < availableSlots[j].min
		})

		// 检查是否有室内和室外场地
		hasIndoor, hasOutdoor := false, false
		for _, slot := range availableSlots {
			if slot.zoneType == "Indoor" {
				hasIndoor = true
			} else if slot.zoneType == "Outdoor" {
				hasOutdoor = true
			}
		}

		msg := "🎾 Kallang Tennis Hub 场地监听\n\n✅ 发现可用场地:\n\n"

		// 添加预定链接
		if hasIndoor {
			msg += "<a href=\"https://thekallang.perfectgym.com/clientportal2/#/FacilityBooking?clubId=1&zoneTypeId=85\">🔗 室内预定</a>"
			if hasOutdoor {
				msg += " | "
			}
		}
		if hasOutdoor {
			msg += "<a href=\"https://thekallang.perfectgym.com/clientportal2/#/FacilityBooking?clubId=1&zoneTypeId=83\">🔗 室外预定</a>"
		}
		msg += "\n\n"

		// 分组显示
		var lastGroupKey string
		weekdayNames := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
		for _, slot := range availableSlots {
			groupKey := fmt.Sprintf("%s %02d:%02d", slot.date, slot.hour, slot.min)
			if groupKey != lastGroupKey {
				if lastGroupKey != "" {
					msg += "\n"
				}
				// 解析日期获取星期几
				t, _ := time.Parse("2006-01-02", slot.date)
				weekday := weekdayNames[t.Weekday()]
				msg += fmt.Sprintf("【%s %s %02d:%02d】\n", weekday, slot.date, slot.hour, slot.min)
				lastGroupKey = groupKey
			}
			msg += fmt.Sprintf("• %s %s (%s)\n", slot.zoneType, slot.court, slot.duration)
		}

		sendTelegramNotification(telegramToken, telegramChatID, msg)
	} else {
		log.Println("No available slots found")
	}
}

func checkZoneAvailability(api *directAPIClient, zoneTypeID int, zoneTypeName string, now time.Time) ([]slotInfo, error) {
	scheduleDate := now.Format("2006-01-02")

	schedule, err := api.getWeeklySchedule(zoneTypeID, scheduleDate)
	if err != nil {
		return nil, fmt.Errorf("get schedule for %s: %w", zoneTypeName, err)
	}

	// 第一遍扫描：找到所有符合时间窗口的可用 slot
	type availableSlotInfo struct {
		startTime   string
		slotTime    time.Time
		slotHour    int
		slotMin     int
		duration    string
		currentDate string
	}
	var candidateSlots []availableSlotInfo

	for _, hourData := range schedule.CalendarData {
		for dayIdx, daySlots := range hourData.ClassesPerDay {
			if dayIdx >= len(schedule.PagerData.Days) {
				break
			}

			for _, slot := range daySlots {
				if !strings.EqualFold(slot.Status, "Bookable") {
					continue
				}

				slotTime, err := time.Parse("2006-01-02T15:04:05", slot.StartTime)
				if err != nil {
					continue
				}

				slotHour := slotTime.Hour()
				slotMin := slotTime.Minute()

				// 检查时间窗口
				dayOfWeek := slotTime.Weekday()
				isWeekend := dayOfWeek == time.Saturday || dayOfWeek == time.Sunday || isSGPublicHoliday(slotTime)

				var inTargetWindow bool
				if zoneTypeID == 85 { // Indoor
					inTargetWindow = (isWeekend && slotHour >= 9 && slotHour < 22) || (!isWeekend && slotHour >= 20 && slotHour < 22)
				} else if zoneTypeID == 83 { // Outdoor
					inTargetWindow = (isWeekend && slotHour >= 16 && slotHour < 22) || (!isWeekend && slotHour >= 20 && slotHour < 22)
				}

				if inTargetWindow {
					candidateSlots = append(candidateSlots, availableSlotInfo{
						startTime:   slot.StartTime,
						slotTime:    slotTime,
						slotHour:    slotHour,
						slotMin:     slotMin,
						duration:    slot.BookingDuration,
						currentDate: schedule.PagerData.Days[dayIdx],
					})
				}
			}
		}
	}

	// 如果没有候选，直接返回
	if len(candidateSlots) == 0 {
		return nil, nil
	}

	// 第二遍：只对有候选的时间调用 Start API（并行）
	type startAPIResult struct {
		courts []string
		slot   availableSlotInfo
		err    error
	}
	resultCh := make(chan startAPIResult, len(candidateSlots))

	for _, candidateSlot := range candidateSlots {
		go func(cs availableSlotInfo) {
			courts, err := api.getStartFacilityInfo(zoneTypeID, cs.startTime)
			resultCh <- startAPIResult{courts, cs, err}
		}(candidateSlot)
	}

	// 收集结果
	var slots []slotInfo
	for i := 0; i < len(candidateSlots); i++ {
		result := <-resultCh
		if result.err != nil {
			continue
		}

		for _, courtName := range result.courts {
			slots = append(slots, slotInfo{
				date:     result.slot.currentDate,
				hour:     result.slot.slotHour,
				min:      result.slot.slotMin,
				zoneType: zoneTypeName,
				court:    courtName,
				duration: result.slot.duration,
			})
		}
	}

	return slots, nil
}

func isSGPublicHoliday(t time.Time) bool {
	// 新加坡公共假日（2026年）
	holidays := []string{
		"2026-01-01", // New Year's Day
		"2026-02-10", // Chinese New Year
		"2026-02-11", // Chinese New Year (in lieu)
		"2026-03-27", // Good Friday
		"2026-04-10", // Hari Raya Puasa
		"2026-05-24", // Vesak Day
		"2026-06-22", // Hari Raya Haji
		"2026-08-09", // National Day
		"2026-10-23", // Deepavali
		"2026-12-25", // Christmas Day
	}

	dateStr := t.Format("2006-01-02")
	for _, holiday := range holidays {
		if dateStr == holiday {
			return true
		}
	}
	return false
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
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
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
