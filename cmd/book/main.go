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

type directAPIClient struct {
	baseURL string
	token   string
	userID  int
	client  *http.Client
}

type directSchedule struct {
	CalendarData []directScheduleHour `json:"CalendarData"`
	PagerData    directPagerData      `json:"PagerData"`
}

type directScheduleHour struct {
	Hour          string                 `json:"Hour"`
	ClassesPerDay [][]directScheduleSlot `json:"ClassesPerDay"`
}

type directScheduleSlot struct {
	BookingDuration string   `json:"BookingDuration"`
	Durations       []string `json:"Durations"`
	Status          string   `json:"Status"`
	StartTime       string   `json:"StartTime"`
	EndTime         string   `json:"EndTime"`
}

type directPagerData struct {
	Days []string `json:"Days"`
}

type bookingRequest struct {
	UserID                int         `json:"UserId"`
	ZoneID                int         `json:"ZoneId"`
	StartTime             string      `json:"StartTime"`
	RequiredNumberOfSlots interface{} `json:"RequiredNumberOfSlots"`
	Duration              int         `json:"Duration"`
}

type startFacilityResponse struct {
	Data struct {
		Zones []struct {
			ID   int    `json:"Id"`
			Name string `json:"Name"`
		} `json:"Zones"`
	} `json:"Data"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("Court Booking bot started")

	// 配置
	targetTimeStr := os.Getenv("TARGET_TIME")         // 例: "2026-09-09T15:00:00"
	targetDurationStr := os.Getenv("TARGET_DURATION") // 例: "60" 或 "120"
	targetZoneTypeID := os.Getenv("TARGET_ZONE_TYPE") // "83" 或 "85"

	if targetTimeStr == "" || targetDurationStr == "" || targetZoneTypeID == "" {
		log.Println("Missing TARGET_TIME, TARGET_DURATION, or TARGET_ZONE_TYPE")
		return
	}

	targetDuration, err := strconv.Atoi(targetDurationStr)
	if err != nil {
		log.Printf("Invalid TARGET_DURATION: %v", err)
		return
	}

	zoneTypeID, err := strconv.Atoi(targetZoneTypeID)
	if err != nil {
		log.Printf("Invalid TARGET_ZONE_TYPE: %v", err)
		return
	}

	baseURL := os.Getenv("PERFECTGYM_BASE_URL")
	if baseURL == "" {
		log.Println("Missing PERFECTGYM_BASE_URL")
		return
	}

	storagePath := ".auth/storage-state.json"
	api, err := newDirectAPIClient(baseURL, storagePath)
	if err != nil {
		log.Printf("Failed to init API client: %v", err)
		return
	}

	// 从目标时间解析日期
	targetTime, err := time.Parse("2006-01-02T15:04:05", targetTimeStr)
	if err != nil {
		log.Printf("Invalid TARGET_TIME format: %v", err)
		return
	}

	// 获取该日期的可用时间表
	scheduleDate := targetTime.Format("2006-01-02")
	schedule, err := api.getWeeklySchedule(zoneTypeID, scheduleDate)
	if err != nil {
		log.Printf("Failed to get schedule: %v", err)
		return
	}

	// 查找目标时间的可用 slot
	var targetSlot *directScheduleSlot
	for _, hourData := range schedule.CalendarData {
		for dayIdx, daySlots := range hourData.ClassesPerDay {
			if dayIdx >= len(schedule.PagerData.Days) {
				break
			}

			for _, slot := range daySlots {
				if slot.StartTime == targetTimeStr && strings.EqualFold(slot.Status, "Bookable") {
					targetSlot = &slot
					break
				}
			}

			if targetSlot != nil {
				break
			}
		}

		if targetSlot != nil {
			break
		}
	}

	if targetSlot == nil {
		log.Printf("Target time slot not found or not bookable: %s", targetTimeStr)
		return
	}

	log.Printf("Found target slot: %s (%s)", targetTimeStr, targetSlot.BookingDuration)

	// 调用 Start API 获取具体的 Court 信息
	courts, err := api.getStartFacilityInfo(zoneTypeID, targetTimeStr)
	if err != nil {
		log.Printf("Failed to get facility info: %v", err)
		return
	}

	if len(courts) == 0 {
		log.Printf("No courts available at %s", targetTimeStr)
		return
	}

	// 预定第一个可用的 court
	courtID := courts[0].ID
	courtName := courts[0].Name

	log.Printf("Booking court: %s (ID: %d) at %s for %d minutes", courtName, courtID, targetTimeStr, targetDuration)

	if err := api.bookFacility(courtID, targetTimeStr, targetDuration); err != nil {
		log.Printf("Failed to book facility: %v", err)
		return
	}

	log.Printf("✅ Successfully booked: %s at %s for %d minutes!", courtName, targetTimeStr, targetDuration)
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

type courtInfo struct {
	ID   int
	Name string
}

func (api *directAPIClient) getStartFacilityInfo(zoneTypeID int, startDateTime string) ([]courtInfo, error) {
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

	var startResp startFacilityResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return nil, fmt.Errorf("decode start response: %w", err)
	}

	var courts []courtInfo
	for _, zone := range startResp.Data.Zones {
		courts = append(courts, courtInfo{ID: zone.ID, Name: zone.Name})
	}

	return courts, nil
}

func (api *directAPIClient) bookFacility(zoneID int, startTime string, duration int) error {
	payload := bookingRequest{
		UserID:                api.userID,
		ZoneID:                zoneID,
		StartTime:             startTime,
		RequiredNumberOfSlots: nil,
		Duration:              duration,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := api.baseURL + "/FacilityBookings/WizardSteps/SetFacilityBookingDetailsWizardStep/Next"
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("authorization", "Bearer "+api.token)
	req.Header.Set("content-type", "application/json;charset=UTF-8")
	req.Header.Set("accept", "application/json, text/plain, */*")
	req.Header.Set("cp-lang", "en")
	req.Header.Set("cp-mode", "desktop")
	req.Header.Set("x-requested-with", "XMLHttpRequest")

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
		return fmt.Errorf("booking failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
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

func newDirectAPIClient(baseURL, storagePath string) (*directAPIClient, error) {
	index := strings.Index(strings.ToLower(baseURL), "/clientportal2")
	if index < 0 {
		return nil, fmt.Errorf("invalid base URL format")
	}
	clientPortalBase := baseURL[:index] + "/clientportal2"

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

func loadAuthToken(storagePath string) (string, error) {
	data, err := os.ReadFile(storagePath)
	if err != nil {
		return "", fmt.Errorf("read auth storage: %w", err)
	}

	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		return "", fmt.Errorf("decode auth storage: %w", err)
	}

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
	}

	return 0, fmt.Errorf("UserId not found in token")
}
