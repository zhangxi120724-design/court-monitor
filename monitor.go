package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type monitorConfig struct {
	PerfectGymBaseURL   string
	TelegramBotToken    string
	TelegramChatID      string
	CheckIntervalMins   int
	Timezone            *time.Location
	DirectAPIClient     *directAPIClient
	LastNotificationAt  map[string]time.Time
	CheckStartHour      int
	CheckEndHour        int
	CheckOnWeekend      bool
	CheckOnWeekday      bool
	WeekdayStartHour    int
	WeekdayEndHour      int
	WeekendStartHour    int
	WeekendEndHour      int
}

type telegramMessage struct {
	ChatID int    `json:"chat_id"`
	Text   string `json:"text"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("load .env: %v", err)
	}

	once := flag.Bool("once", false, "Run check once and exit")
	flag.Parse()

	cfg, err := loadMonitorConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	log.Printf("Monitor started: checking every %d minutes", cfg.CheckIntervalMins)
	log.Printf("Weekday slots: %d:00-%d:00", cfg.WeekdayStartHour, cfg.WeekdayEndHour)
	log.Printf("Weekend slots: %d:00-%d:00", cfg.WeekendStartHour, cfg.WeekendEndHour)

	if *once {
		if err := runMonitorCheck(cfg); err != nil {
			log.Fatalf("monitor check failed: %v", err)
		}
		return
	}

	ticker := time.NewTicker(time.Duration(cfg.CheckIntervalMins) * time.Minute)
	defer ticker.Stop()

	for {
		if err := runMonitorCheck(cfg); err != nil {
			log.Printf("monitor check failed: %v", err)
		}
		<-ticker.C
	}
}

func loadMonitorConfig() (*monitorConfig, error) {
	location := time.Now().Location()
	tzName := strings.TrimSpace(os.Getenv("BOOKING_TIMEZONE"))
	if tzName != "" {
		var err error
		location, err = time.LoadLocation(tzName)
		if err != nil {
			return nil, fmt.Errorf("load timezone %q: %w", tzName, err)
		}
	}

	storagePath := ".auth/storage-state.json"
	api, err := newDirectAPIClient(config{
		BaseURL:  requireEnv("PERFECTGYM_BASE_URL"),
		Timezone: location,
	}, storagePath)
	if err != nil {
		return nil, fmt.Errorf("init direct API client: %w", err)
	}

	return &monitorConfig{
		PerfectGymBaseURL:  requireEnv("PERFECTGYM_BASE_URL"),
		TelegramBotToken:   requireEnv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:     requireEnv("TELEGRAM_CHAT_ID"),
		CheckIntervalMins:  parseInt("MONITOR_CHECK_INTERVAL_MINS", 5),
		Timezone:           location,
		DirectAPIClient:    api,
		LastNotificationAt: make(map[string]time.Time),
		WeekdayStartHour:   parseInt("MONITOR_WEEKDAY_START_HOUR", 20),
		WeekdayEndHour:     parseInt("MONITOR_WEEKDAY_END_HOUR", 22),
		WeekendStartHour:   parseInt("MONITOR_WEEKEND_START_HOUR", 17),
		WeekendEndHour:     parseInt("MONITOR_WEEKEND_END_HOUR", 22),
	}, nil
}

func runMonitorCheck(cfg *monitorConfig) error {
	now := time.Now().In(cfg.Timezone)
	log.Printf("Checking at %s (local time)", now.Format(time.RFC3339))

	zoneTypes, err := cfg.DirectAPIClient.getClubZoneTypes()
	if err != nil {
		return fmt.Errorf("fetch zone types: %w", err)
	}

	slots, err := findAvailableSlots(cfg, zoneTypes, now)
	if err != nil {
		return fmt.Errorf("scan slots: %w", err)
	}

	if len(slots) > 0 {
		log.Printf("Found %d available slot(s)", len(slots))
		for _, slot := range slots {
			// 只在目标时间窗口内发送通知
			if isWithinTargetTimeWindow(now, cfg) {
				if err := sendAvailabilityNotification(cfg, slot); err != nil {
					log.Printf("send notification for %s: %v", slot, err)
				}
			} else {
				// 在目标时间外找到的空场地，记录但不通知
				log.Printf("Slot available outside target window: %s", slot)
			}
		}
	} else {
		log.Printf("No available slots found")
	}
	return nil
}

func isWithinTargetTimeWindow(now time.Time, cfg *monitorConfig) bool {
	hour := now.Hour()
	weekday := now.Weekday()

	isWeekend := weekday == time.Saturday || weekday == time.Sunday
	isWeekday := weekday >= time.Monday && weekday <= time.Friday

	if isWeekend {
		return hour >= cfg.WeekendStartHour && hour < cfg.WeekendEndHour
	}
	if isWeekday {
		return hour >= cfg.WeekdayStartHour && hour < cfg.WeekdayEndHour
	}
	return false
}

func findAvailableSlots(cfg *monitorConfig, zoneTypes map[string]directZoneType, now time.Time) ([]string, error) {
	var slots []string

	indoorZoneTypeID := 85
	indoorZoneType, ok := zoneTypes[strconv.Itoa(indoorZoneTypeID)]
	if ok {
		indoorSlots, err := checkZoneAvailability(cfg, indoorZoneTypeID, indoorZoneType, "indoor", now)
		if err != nil {
			log.Printf("check indoor slots: %v", err)
		} else {
			slots = append(slots, indoorSlots...)
		}
	}

	outdoorZoneTypeID := 83
	outdoorZoneType, ok := zoneTypes[strconv.Itoa(outdoorZoneTypeID)]
	if ok {
		outdoorSlots, err := checkZoneAvailability(cfg, outdoorZoneTypeID, outdoorZoneType, "outdoor", now)
		if err != nil {
			log.Printf("check outdoor slots: %v", err)
		} else {
			slots = append(slots, outdoorSlots...)
		}
	}

	return slots, nil
}

func checkZoneAvailability(cfg *monitorConfig, zoneTypeID int, zoneType directZoneType, zoneTypeName string, now time.Time) ([]string, error) {
	var slots []string

	for _, zone := range zoneType.Zones {
		scheduleDate := now.Format("2006-01-02")
		schedule, err := cfg.DirectAPIClient.getWeeklySchedule(zoneTypeID, zone.ID, scheduleDate)
		if err != nil {
			log.Printf("get schedule for %s %s: %v", zoneTypeName, zone.Name, err)
			continue
		}

		for _, hour := range schedule.CalendarData {
			for _, daySlots := range hour.ClassesPerDay {
				for _, slot := range daySlots {
					if strings.EqualFold(slot.Status, "Bookable") {
						hour, minute := parseTimeString(slot.StartTime)
						if isWithinTargetHours(hour, minute, cfg, now) {
							slotDesc := fmt.Sprintf("%s %s at %s (%s)", zoneTypeName, zone.Name, slot.StartTime, slot.BookingDuration)
							slots = append(slots, slotDesc)
						}
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

func isWithinTargetHours(hour, minute int, cfg *monitorConfig, now time.Time) bool {
	isWeekend := now.Weekday() == time.Saturday || now.Weekday() == time.Sunday

	if isWeekend {
		startHour := cfg.WeekendStartHour
		endHour := cfg.WeekendEndHour
		slotTime := hour + (minute / 60)
		return slotTime >= startHour && slotTime < endHour
	}

	startHour := cfg.WeekdayStartHour
	endHour := cfg.WeekdayEndHour
	slotTime := hour + (minute / 60)
	return slotTime >= startHour && slotTime < endHour
}

func sendAvailabilityNotification(cfg *monitorConfig, slotDesc string) error {
	key := slotDesc
	lastNotif, notified := cfg.LastNotificationAt[key]
	if notified && time.Since(lastNotif) < 1*time.Hour {
		log.Printf("Skip duplicate notification for %s (notified %v ago)", slotDesc, time.Since(lastNotif).Round(time.Minute))
		return nil
	}

	text := fmt.Sprintf("🎾 Kallang Tennis Hub 有场地可预订！\n\n场地: %s\n\n请立即前往 PerfectGym 进行预订！", slotDesc)

	if err := sendTelegramMessage(cfg, text); err != nil {
		return fmt.Errorf("send telegram: %w", err)
	}

	cfg.LastNotificationAt[key] = time.Now()
	log.Printf("Sent Telegram notification for: %s", slotDesc)
	return nil
}

func sendTelegramMessage(cfg *monitorConfig, text string) error {
	chatID, err := strconv.ParseInt(cfg.TelegramChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram chat id: %w", err)
	}

	msg := telegramMessage{
		ChatID: int(chatID),
		Text:   text,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.TelegramBotToken)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram api returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func requireEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("missing required environment variable: %s", name)
	}
	return value
}

func parseInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
