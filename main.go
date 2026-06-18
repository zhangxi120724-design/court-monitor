package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/playwright-community/playwright-go"
)

const (
	playwrightVersion      = "v0.4702.0"
	defaultLoginURL        = "https://thekallang.perfectgym.com/clientportal2/"
	defaultTimezone        = "Asia/Singapore"
	defaultTargetOffset    = 7
	defaultBookingLeadDays = 7
	defaultRefreshAttempts = 4
	defaultRefreshSpacing  = 800 * time.Millisecond
	defaultPrepareLead     = 90 * time.Second
	defaultManualLoginWait = 5 * time.Minute
	storageDir             = ".auth"
	storageFile            = "storage-state.json"
)

var (
	loginRequiredPattern = regexp.MustCompile(`(?i)sign in|log in|login|password`)
	calendarDatePattern  = regexp.MustCompile(`(?i)\b(\d{1,2})\s*/\s*(\d{1,2})\b`)
)

type config struct {
	BaseURL             string
	LoginURL            string
	TargetDate          string
	TargetTimeText      string
	TargetCourtKeyword  string
	Targets             []bookingTargetConfig
	PreferredDurations  []int
	Email               string
	Password            string
	Headless            bool
	RefreshAttempts     int
	RefreshSpacing      time.Duration
	PrepareLead         time.Duration
	ManualLoginWait     time.Duration
	DirectAPIEarly      time.Duration
	DirectAPIWorkers    int
	Timezone            *time.Location
	StartAt             *time.Time
	DirectAPI           bool
	NetworkCapturePath  string
	NetworkCaptureLimit int
}

type slotMatch struct {
	page      playwright.Page
	candidate playwright.Locator
	action    playwright.Locator
	text      string
	target    string
}

type bookingTargetConfig struct {
	Name            string
	URL             string
	CourtType       string
	CourtKeyword    string
	CourtPriorities []courtPriority
}

type bookingTargetSession struct {
	config bookingTargetConfig
	page   playwright.Page
}

type courtPriority struct {
	kind   string
	number int
	label  string
}

type scoredSlot struct {
	slot         *slotMatch
	courtRank    int
	durationRank int
	ordinal      int
}

type storageState struct {
	Cookies []storageCookie `json:"cookies"`
	Origins []storageOrigin `json:"origins"`
}

type storageCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type storageOrigin struct {
	Origin       string              `json:"origin"`
	LocalStorage []storageLocalValue `json:"localStorage"`
}

type storageLocalValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type directAPIClient struct {
	baseURL string
	token   string
	userID  int
	client  *http.Client
}

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

type directSlotCandidate struct {
	target     bookingTargetConfig
	zoneTypeID int
	zone       directZone
	duration   int
	startTime  string
}

type directZoneScanResult struct {
	index    int
	zone     directZone
	duration int
	err      error
}

type directStartResponse struct {
	Data directStartData `json:"Data"`
}

type directStartData struct {
	Name   string       `json:"Name"`
	ZoneID int          `json:"ZoneId"`
	Zones  []directZone `json:"Zones"`
}

type directBookingRuleResponse struct {
	Data directBookingRuleData `json:"Data"`
}

type directBookingRuleData struct {
	RuleID int                          `json:"RuleId"`
	Rules  map[string]directBookingRule `json:"Rules"`
}

type directBookingRule struct {
	ID                      int     `json:"Id"`
	RuleName                string  `json:"RuleName"`
	ProductName             string  `json:"ProductName"`
	Price                   float64 `json:"Price"`
	HasUserRequiredProducts bool    `json:"HasUserRequiredProducts"`
	CanBeBookedNow          bool    `json:"CanBeBookedNow"`
	CanBuyProductAndBook    bool    `json:"CanBuyProductAndBook"`
	ProductsQuantityToBuy   int     `json:"ProductsQuantityToBuy"`
	AvailableProducts       int     `json:"AvailableProducts"`
	IsPaymentRequired       bool    `json:"IsPaymentRequired"`
}

type directCartQuantity struct {
	TotalQuantity float64 `json:"TotalQuantity"`
}

type networkCaptureRecord struct {
	Type          string            `json:"type"`
	At            string            `json:"at"`
	Method        string            `json:"method,omitempty"`
	URL           string            `json:"url"`
	ResourceType  string            `json:"resourceType,omitempty"`
	Status        int               `json:"status,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	PostData      string            `json:"postData,omitempty"`
	Body          string            `json:"body,omitempty"`
	BodyTruncated bool              `json:"bodyTruncated,omitempty"`
	Error         string            `json:"error,omitempty"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("load .env: %v", err)
	}

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	var runErr error
	if cfg.DirectAPI {
		runErr = runDirectAPI(cfg)
	} else {
		runErr = run(cfg)
	}
	if runErr != nil {
		log.Fatalf("booking failed: %v", runErr)
	}
}

func run(cfg config) error {
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}

	if cfg.StartAt != nil {
		log.Printf("Booking start is set to %s", cfg.StartAt.In(cfg.Timezone).Format(time.RFC3339))
		waitUntil(cfg.StartAt.Add(-cfg.PrepareLead), "prepare booking session")
	}

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf(
			"start Playwright: %w\ninstall driver with: go run github.com/playwright-community/playwright-go/cmd/playwright@%s install chromium",
			err,
			playwrightVersion,
		)
	}
	defer func() {
		if stopErr := pw.Stop(); stopErr != nil {
			log.Printf("stop Playwright: %v", stopErr)
		}
	}()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(cfg.Headless),
	})
	if err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}
	defer func() {
		if closeErr := browser.Close(); closeErr != nil {
			log.Printf("close browser: %v", closeErr)
		}
	}()

	contextOptions := playwright.BrowserNewContextOptions{
		TimezoneId: playwright.String(cfg.Timezone.String()),
	}
	storagePath := filepath.Join(storageDir, storageFile)
	if _, err := os.Stat(storagePath); err == nil {
		contextOptions.StorageStatePath = playwright.String(storagePath)
	}

	context, err := browser.NewContext(contextOptions)
	if err != nil {
		return fmt.Errorf("create context: %w", err)
	}
	var stopNetworkCapture func()
	defer func() {
		if closeErr := context.Close(); closeErr != nil {
			log.Printf("close context: %v", closeErr)
		}
		if stopNetworkCapture != nil {
			time.Sleep(300 * time.Millisecond)
			stopNetworkCapture()
		}
	}()

	if cfg.NetworkCapturePath != "" {
		stop, err := startNetworkCapture(context, cfg.NetworkCapturePath, cfg.NetworkCaptureLimit)
		if err != nil {
			return err
		}
		stopNetworkCapture = stop
		log.Printf("Network capture enabled: %s", cfg.NetworkCapturePath)
	}

	page, err := context.NewPage()
	if err != nil {
		return fmt.Errorf("create page: %w", err)
	}

	if err := ensureLoggedIn(page, context, cfg, storagePath); err != nil {
		return err
	}

	targets, err := prepareBookingTargets(context, page, cfg)
	if err != nil {
		return err
	}

	if cfg.StartAt != nil {
		waitUntil(*cfg.StartAt, "booking start")
	}

	slot, err := pollForTargetSlot(targets, cfg)
	if err != nil {
		return err
	}
	if slot == nil {
		return fmt.Errorf("no matching slot found after %d refresh attempts", cfg.RefreshAttempts)
	}

	if err := addSlotToCart(slot, cfg); err != nil {
		return err
	}
	if _, err := context.StorageState(storagePath); err != nil {
		return fmt.Errorf("save storage state: %w", err)
	}

	log.Println("Success: matching facility slot was added to cart.")
	return nil
}

func loadConfig() (config, error) {
	locationName := strings.TrimSpace(getEnvDefault("BOOKING_TIMEZONE", defaultTimezone))
	location, err := time.LoadLocation(locationName)
	if err != nil {
		return config{}, fmt.Errorf("load booking timezone %q: %w", locationName, err)
	}

	targetDate, err := resolveTargetDate(location)
	if err != nil {
		return config{}, err
	}

	targetTimeText := strings.TrimSpace(os.Getenv("TARGET_TIME_TEXT"))
	startAt, err := resolveBookingStart(targetDate, targetTimeText, location)
	if err != nil {
		return config{}, err
	}

	preferredDurations, err := parseDurationPriority(getEnvDefault("BOOKING_DURATION_PRIORITY", "120,60"))
	if err != nil {
		return config{}, err
	}

	baseURL := requireEnv("PERFECTGYM_BASE_URL")
	indoorURL := strings.TrimSpace(getEnvDefault("INDOOR_BOOKING_URL", withZoneTypeID(baseURL, 85)))
	outdoorURL := strings.TrimSpace(getEnvDefault("OUTDOOR_BOOKING_URL", withZoneTypeID(baseURL, 83)))

	var targets []bookingTargetConfig
	targets = append(targets, bookingTargetConfig{
		Name:      "indoor",
		URL:       indoorURL,
		CourtType: "indoor",
	})

	if parseBool("ENABLE_OUTDOOR_FALLBACK", true) {
		outdoorPriorities, err := parseCourtPriorities(getEnvDefault("OUTDOOR_COURT_PRIORITY", "8-15"))
		if err != nil {
			return config{}, err
		}
		targets = append(targets, bookingTargetConfig{
			Name:            "outdoor",
			URL:             outdoorURL,
			CourtType:       "outdoor",
			CourtPriorities: outdoorPriorities,
		})
	}

	return config{
		BaseURL:             baseURL,
		LoginURL:            strings.TrimSpace(getEnvDefault("PERFECTGYM_LOGIN_URL", defaultLoginURL)),
		TargetDate:          targetDate,
		TargetTimeText:      strings.ToLower(targetTimeText),
		TargetCourtKeyword:  strings.ToLower(strings.TrimSpace(os.Getenv("TARGET_COURT_KEYWORD"))),
		Targets:             targets,
		PreferredDurations:  preferredDurations,
		Email:               strings.TrimSpace(os.Getenv("PERFECTGYM_EMAIL")),
		Password:            strings.TrimSpace(os.Getenv("PERFECTGYM_PASSWORD")),
		Headless:            parseBool("HEADLESS", false),
		RefreshAttempts:     parseInt("REFRESH_ATTEMPTS", defaultRefreshAttempts),
		RefreshSpacing:      parseDurationMilliseconds("REFRESH_SPACING_MS", defaultRefreshSpacing),
		PrepareLead:         parseDurationSeconds("BOOKING_PREPARE_SECONDS", defaultPrepareLead),
		ManualLoginWait:     parseDurationSeconds("MANUAL_LOGIN_TIMEOUT_SECONDS", defaultManualLoginWait),
		DirectAPIEarly:      parseDurationMilliseconds("DIRECT_API_EARLY_MS", 0),
		DirectAPIWorkers:    parseInt("DIRECT_API_WORKERS", 1),
		Timezone:            location,
		StartAt:             startAt,
		DirectAPI:           parseBool("DIRECT_API", false),
		NetworkCapturePath:  strings.TrimSpace(os.Getenv("NETWORK_CAPTURE_PATH")),
		NetworkCaptureLimit: parseInt("NETWORK_CAPTURE_BODY_LIMIT", 20_000),
	}, nil
}

func runDirectAPI(cfg config) error {
	storagePath := filepath.Join(storageDir, storageFile)
	api, err := newDirectAPIClient(cfg, storagePath)
	if err != nil {
		return err
	}

	var zoneTypes map[string]directZoneType
	if cfg.StartAt != nil {
		var err error
		zoneTypes, err = api.getClubZoneTypes()
		if err != nil {
			log.Printf("prefetch club zone types failed; will retry at booking start: %v", err)
		} else {
			log.Printf("Prefetched %d club zone type(s) before booking start.", len(zoneTypes))
		}
	}

	if cfg.StartAt != nil {
		waitAt := cfg.StartAt.Add(-cfg.DirectAPIEarly)
		if cfg.DirectAPIEarly > 0 {
			log.Printf(
				"Direct API booking start is set to %s; first attempt starts %s early at %s",
				cfg.StartAt.In(cfg.Timezone).Format(time.RFC3339),
				cfg.DirectAPIEarly,
				waitAt.In(cfg.Timezone).Format(time.RFC3339Nano),
			)
		} else {
			log.Printf("Direct API booking start is set to %s", cfg.StartAt.In(cfg.Timezone).Format(time.RFC3339))
		}
		waitUntil(waitAt, "direct API booking start")
	}

	if zoneTypes == nil {
		var err error
		zoneTypes, err = api.getClubZoneTypes()
		if err != nil {
			return err
		}
	}

	attempts := cfg.RefreshAttempts
	if attempts < 1 {
		attempts = 1
	}
	workers := cfg.DirectAPIWorkers
	if workers < 1 {
		workers = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(cfg.RefreshSpacing)
		}
		if attempts > 1 {
			log.Printf("Direct API attempt %d/%d with %d schedule worker(s)", attempt, attempts, workers)
		}

		booked, err := api.tryDirectBooking(cfg, zoneTypes)
		if err == nil && booked {
			log.Println("Success: direct API booking flow completed.")
			return nil
		}
		if err != nil {
			lastErr = err
			log.Printf("direct API attempt %d failed: %v", attempt, err)
		}
	}

	if lastErr != nil {
		return fmt.Errorf("direct API booking failed after %d attempts: %w", attempts, lastErr)
	}
	return fmt.Errorf("direct API found no matching slot for %s %s after %d attempts", cfg.TargetDate, cfg.TargetTimeText, attempts)
}

func (api *directAPIClient) tryDirectBooking(cfg config, zoneTypes map[string]directZoneType) (bool, error) {
	for _, target := range cfg.Targets {
		candidate, err := api.findDirectSlotCandidate(cfg, target, zoneTypes)
		if err != nil {
			log.Printf("direct %s scan: %v", target.Name, err)
			continue
		}
		if candidate == nil {
			continue
		}

		log.Printf(
			"Direct API selected %s: %s (zoneId=%d) at %s for %d minutes",
			candidate.target.Name,
			candidate.zone.Name,
			candidate.zone.ID,
			candidate.startTime,
			candidate.duration,
		)
		if err := api.bookDirectSlot(cfg, *candidate); err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

func newDirectAPIClient(cfg config, storagePath string) (*directAPIClient, error) {
	baseURL, err := clientPortalBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}

	token, err := loadAuthToken(storagePath)
	if err != nil {
		return nil, err
	}

	userID, err := userIDFromAuthToken(token)
	if err != nil {
		return nil, err
	}

	return &directAPIClient{
		baseURL: baseURL,
		token:   token,
		userID:  userID,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}, nil
}

func (api *directAPIClient) getClubZoneTypes() (map[string]directZoneType, error) {
	var zoneTypes map[string]directZoneType
	if err := api.postJSON("/FacilityBookings/FacilityCalendar/GetClubZoneTypes", "#/FacilityBooking?clubId=1", map[string]int{
		"clubId": 1,
	}, nil, &zoneTypes); err != nil {
		return nil, fmt.Errorf("get club zone types: %w", err)
	}
	return zoneTypes, nil
}

func (api *directAPIClient) findDirectSlotCandidate(cfg config, target bookingTargetConfig, zoneTypes map[string]directZoneType) (*directSlotCandidate, error) {
	zoneTypeID, err := zoneTypeIDFromURL(target.URL)
	if err != nil {
		return nil, err
	}

	zoneType, ok := zoneTypes[strconv.Itoa(zoneTypeID)]
	if !ok {
		return nil, fmt.Errorf("zone type %d not found in club zone types", zoneTypeID)
	}

	zones := directZoneCandidates(target, zoneType)
	if len(zones) == 0 {
		return nil, fmt.Errorf("no direct zone candidates for %s (%s)", target.Name, zoneType.Name)
	}

	targetStart, err := directTargetStartTime(cfg)
	if err != nil {
		return nil, err
	}
	scheduleDate, err := directScheduleDate(cfg)
	if err != nil {
		return nil, err
	}

	workers := cfg.DirectAPIWorkers
	if workers > len(zones) {
		workers = len(zones)
	}
	if workers > 1 {
		return api.findDirectSlotCandidateParallel(cfg, target, zoneTypeID, zones, targetStart, scheduleDate, workers)
	}

	for _, zone := range zones {
		schedule, err := api.getWeeklySchedule(zoneTypeID, zone.ID, scheduleDate)
		if err != nil {
			log.Printf("direct schedule %s zone %d: %v", target.Name, zone.ID, err)
			continue
		}

		if duration, ok := matchingDirectDuration(schedule, targetStart, cfg.PreferredDurations); ok {
			return &directSlotCandidate{
				target:     target,
				zoneTypeID: zoneTypeID,
				zone:       zone,
				duration:   duration,
				startTime:  targetStart,
			}, nil
		}
	}

	return nil, nil
}

func (api *directAPIClient) findDirectSlotCandidateParallel(cfg config, target bookingTargetConfig, zoneTypeID int, zones []directZone, targetStart, scheduleDate string, workers int) (*directSlotCandidate, error) {
	type zoneJob struct {
		index int
		zone  directZone
	}

	jobs := make(chan zoneJob, len(zones))
	results := make(chan directZoneScanResult, len(zones))
	for index, zone := range zones {
		jobs <- zoneJob{index: index, zone: zone}
	}
	close(jobs)

	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				schedule, err := api.getWeeklySchedule(zoneTypeID, job.zone.ID, scheduleDate)
				if err != nil {
					results <- directZoneScanResult{index: job.index, zone: job.zone, err: err}
					continue
				}
				duration, ok := matchingDirectDuration(schedule, targetStart, cfg.PreferredDurations)
				if !ok {
					results <- directZoneScanResult{index: job.index, zone: job.zone}
					continue
				}
				results <- directZoneScanResult{index: job.index, zone: job.zone, duration: duration}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var best *directZoneScanResult
	for result := range results {
		if result.err != nil {
			log.Printf("direct schedule %s zone %d: %v", target.Name, result.zone.ID, result.err)
			continue
		}
		if result.duration == 0 {
			continue
		}

		if len(target.CourtPriorities) == 0 {
			return &directSlotCandidate{
				target:     target,
				zoneTypeID: zoneTypeID,
				zone:       result.zone,
				duration:   result.duration,
				startTime:  targetStart,
			}, nil
		}

		resultCopy := result
		if best == nil || resultCopy.index < best.index {
			best = &resultCopy
		}
	}

	if best == nil {
		return nil, nil
	}
	return &directSlotCandidate{
		target:     target,
		zoneTypeID: zoneTypeID,
		zone:       best.zone,
		duration:   best.duration,
		startTime:  targetStart,
	}, nil
}

func (api *directAPIClient) getWeeklySchedule(zoneTypeID, zoneID int, scheduleDate string) (*directSchedule, error) {
	hash := fmt.Sprintf("#/FacilityBooking?clubId=1&zoneTypeId=%d&date=%s", zoneTypeID, scheduleDate)
	payload := map[string]interface{}{
		"clubId":     1,
		"date":       scheduleDate,
		"zoneTypeId": strconv.Itoa(zoneTypeID),
		"zoneId":     zoneID,
		"daysInWeek": 6,
	}

	var schedule directSchedule
	if err := api.postJSON("/FacilityBookings/FacilityCalendar/GetWeeklySchedule", hash, payload, nil, &schedule); err != nil {
		return nil, err
	}
	return &schedule, nil
}

func (api *directAPIClient) bookDirectSlot(cfg config, candidate directSlotCandidate) error {
	scheduleDate, err := directScheduleDate(cfg)
	if err != nil {
		return err
	}

	hash := fmt.Sprintf("#/FacilityBooking?clubId=1&zoneTypeId=%d&date=%s", candidate.zoneTypeID, scheduleDate)
	redirectURL := api.baseURL + "/" + hash
	startURL := api.baseURL + "/FacilityBookings/BuyProductBeforeBookingFacility/Start"
	values := url.Values{}
	values.Set("RedirectUrl", redirectURL)
	values.Set("clubId", "1")
	values.Set("startDate", candidate.startTime)
	values.Set("zoneTypeId", strconv.Itoa(candidate.zoneTypeID))
	startURL += "?" + values.Encode()

	startHeaders := map[string]string{
		"BAF-WIZARD-CURRENTURL": fmt.Sprintf("%s/%s&sessionId=undefined", api.baseURL, hash),
	}
	var startResponse directStartResponse
	startResp, _, err := api.doJSON(http.MethodGet, startURL, hash, nil, startHeaders, &startResponse)
	if err != nil {
		return fmt.Errorf("direct booking start: %w", err)
	}
	sessionID := startResp.Header.Get("cp-buy-product-before-booking-fb-session-id")
	if sessionID == "" {
		return fmt.Errorf("direct booking start did not return session id")
	}
	if !directStartIncludesZone(startResponse, candidate.zone.ID) {
		return fmt.Errorf(
			"direct booking start did not offer %s (zoneId=%d); offered zones: %s",
			candidate.zone.Name,
			candidate.zone.ID,
			directStartZoneSummary(startResponse),
		)
	}

	currentURL := fmt.Sprintf("%s/%s&sessionId=%s", api.baseURL, hash, sessionID)
	sessionHeader := "CP-BUY-PRODUCT-BEFORE-BOOKING-FB-SESSION-ID"
	payload := map[string]interface{}{
		"UserId":                api.userID,
		"ZoneId":                candidate.zone.ID,
		"StartTime":             candidate.startTime,
		"RequiredNumberOfSlots": nil,
		"Duration":              candidate.duration,
	}
	headers := map[string]string{
		"BAF-WIZARD-CURRENTURL": currentURL,
		sessionHeader:           sessionID,
	}

	var ruleResponse directBookingRuleResponse
	if err := api.postJSON("/FacilityBookings/WizardSteps/SetFacilityBookingDetailsWizardStep/Next", hash, payload, headers, &ruleResponse); err != nil {
		return fmt.Errorf("direct booking next: %w", err)
	}

	rule, err := selectedDirectBookingRule(ruleResponse)
	if err != nil {
		return err
	}

	log.Printf(
		"Direct API selected booking rule: %s (ruleId=%d, price=%.2f, buyAndBook=%t, bookNow=%t)",
		rule.RuleName,
		rule.ID,
		rule.Price,
		rule.CanBuyProductAndBook,
		rule.CanBeBookedNow,
	)

	rulePayload := map[string]interface{}{
		"RuleId":                  rule.ID,
		"ruleId":                  rule.ID,
		"HasUserRequiredProducts": rule.HasUserRequiredProducts,
		"OtherCalendarEventBookedAtRequestedTime": false,
	}
	var cartBefore float64
	if rule.CanBuyProductAndBook {
		rulePayload["ShouldBuyRequiredProductOnDebit"] = true
		cartBefore, err = api.getCartQuantity(hash)
		if err != nil {
			return fmt.Errorf("get cart quantity before add-to-cart: %w", err)
		}
	} else if !rule.CanBeBookedNow {
		return fmt.Errorf("selected booking rule %d cannot be booked now or added to cart", rule.ID)
	}

	if err := api.postJSON("/FacilityBookings/WizardSteps/ChooseBookingRuleStep/Next", hash, rulePayload, headers, nil); err != nil {
		return fmt.Errorf("direct booking add to cart: %w", err)
	}
	if rule.CanBuyProductAndBook {
		cartAfter, err := api.getCartQuantity(hash)
		if err != nil {
			return fmt.Errorf("get cart quantity after add-to-cart: %w", err)
		}
		if cartAfter <= cartBefore {
			return fmt.Errorf("direct add-to-cart did not increase cart quantity (before %.0f, after %.0f)", cartBefore, cartAfter)
		}
		log.Printf("Direct API added booking to cart (cart quantity %.0f -> %.0f).", cartBefore, cartAfter)
	}
	return nil
}

func selectedDirectBookingRule(response directBookingRuleResponse) (directBookingRule, error) {
	if response.Data.RuleID == 0 {
		return directBookingRule{}, fmt.Errorf("direct booking did not return a booking rule")
	}
	if len(response.Data.Rules) == 0 {
		return directBookingRule{}, fmt.Errorf("direct booking returned ruleId %d but no rules", response.Data.RuleID)
	}

	rule, ok := response.Data.Rules[strconv.Itoa(response.Data.RuleID)]
	if !ok {
		return directBookingRule{}, fmt.Errorf("direct booking rule %d not found in response", response.Data.RuleID)
	}
	return rule, nil
}

func directStartIncludesZone(response directStartResponse, zoneID int) bool {
	if response.Data.ZoneID == zoneID {
		return true
	}
	for _, zone := range response.Data.Zones {
		if zone.ID == zoneID {
			return true
		}
	}
	return false
}

func directStartZoneSummary(response directStartResponse) string {
	zones := make([]string, 0, len(response.Data.Zones)+1)
	for _, zone := range response.Data.Zones {
		zones = append(zones, fmt.Sprintf("%s (%d)", zone.Name, zone.ID))
	}
	if len(zones) == 0 && response.Data.ZoneID != 0 {
		zones = append(zones, fmt.Sprintf("%s (%d)", response.Data.Name, response.Data.ZoneID))
	}
	if len(zones) == 0 {
		return "none"
	}
	return strings.Join(zones, ", ")
}

func (api *directAPIClient) getCartQuantity(hash string) (float64, error) {
	var quantity directCartQuantity
	endpoint := api.baseURL + "/Shopping/ShoppingCart/GetTotalQuantity"
	if _, _, err := api.doJSON(http.MethodGet, endpoint, hash, nil, nil, &quantity); err != nil {
		return 0, err
	}
	return quantity.TotalQuantity, nil
}

func (api *directAPIClient) postJSON(path, hash string, payload interface{}, headers map[string]string, out interface{}) error {
	endpoint := api.baseURL + path
	_, _, err := api.doJSON(http.MethodPost, endpoint, hash, payload, headers, out)
	return err
}

func (api *directAPIClient) doJSON(method, endpoint, hash string, payload interface{}, extraHeaders map[string]string, out interface{}) (*http.Response, []byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("authorization", "Bearer "+api.token)
	req.Header.Set("accept", "application/json, text/plain, */*")
	req.Header.Set("cp-lang", "en")
	req.Header.Set("cp-mode", "desktop")
	req.Header.Set("referer", api.baseURL+"/")
	req.Header.Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36")
	req.Header.Set("x-requested-with", "XMLHttpRequest")
	if hash != "" {
		req.Header.Set("x-hash", hash)
	}
	if payload != nil {
		req.Header.Set("content-type", "application/json;charset=UTF-8")
	}
	for name, value := range extraHeaders {
		req.Header.Set(name, value)
	}

	resp, err := api.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp, responseBody, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, responseBody, fmt.Errorf("%s %s returned %d: %s", method, endpoint, resp.StatusCode, shortLogText(string(responseBody), 500))
	}
	if out != nil && len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, out); err != nil {
			return resp, responseBody, fmt.Errorf("decode %s response: %w; body=%s", endpoint, err, shortLogText(string(responseBody), 500))
		}
	}
	return resp, responseBody, nil
}

func directZoneCandidates(target bookingTargetConfig, zoneType directZoneType) []directZone {
	zones := make([]directZone, 0, len(zoneType.Zones))
	for _, zone := range zoneType.Zones {
		if target.CourtKeyword != "" && !strings.Contains(strings.ToLower(zone.Name), target.CourtKeyword) {
			continue
		}
		zones = append(zones, zone)
	}

	sort.Slice(zones, func(i, j int) bool {
		left := extractCourtNumber(strings.ToLower(zones[i].Name))
		right := extractCourtNumber(strings.ToLower(zones[j].Name))
		if left != right {
			return left < right
		}
		return zones[i].ID < zones[j].ID
	})

	if len(target.CourtPriorities) == 0 {
		return zones
	}

	var prioritized []directZone
	used := make(map[int]bool, len(zones))
	for _, priority := range target.CourtPriorities {
		for _, zone := range zones {
			if used[zone.ID] || extractCourtNumber(strings.ToLower(zone.Name)) != priority.number {
				continue
			}
			prioritized = append(prioritized, zone)
			used[zone.ID] = true
			break
		}
	}
	return prioritized
}

func matchingDirectDuration(schedule *directSchedule, targetStart string, preferredDurations []int) (int, bool) {
	available := make(map[int]bool)
	for _, hour := range schedule.CalendarData {
		for _, daySlots := range hour.ClassesPerDay {
			for _, slot := range daySlots {
				if slot.StartTime != targetStart || !strings.EqualFold(slot.Status, "Bookable") {
					continue
				}
				for _, duration := range slot.Durations {
					if minutes, ok := isoDurationMinutes(duration); ok {
						available[minutes] = true
					}
				}
				if minutes, ok := isoDurationMinutes(slot.BookingDuration); ok {
					available[minutes] = true
				}
			}
		}
	}

	for _, preferred := range preferredDurations {
		if available[preferred] {
			return preferred, true
		}
	}
	return 0, false
}

func isoDurationMinutes(value string) (int, bool) {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" || !strings.HasPrefix(value, "PT") {
		return 0, false
	}
	value = strings.TrimPrefix(value, "PT")
	if strings.HasSuffix(value, "H") {
		hours, err := strconv.Atoi(strings.TrimSuffix(value, "H"))
		if err == nil {
			return hours * 60, true
		}
	}
	if strings.HasSuffix(value, "M") {
		minutes, err := strconv.Atoi(strings.TrimSuffix(value, "M"))
		if err == nil {
			return minutes, true
		}
	}
	return 0, false
}

func directTargetStartTime(cfg config) (string, error) {
	day, err := time.ParseInLocation("2006-01-02", cfg.TargetDate, cfg.Timezone)
	if err != nil {
		return "", err
	}
	slotTime, err := parseTimeOfDay(cfg.TargetTimeText)
	if err != nil {
		return "", err
	}
	return time.Date(day.Year(), day.Month(), day.Day(), slotTime.Hour(), slotTime.Minute(), 0, 0, cfg.Timezone).Format("2006-01-02T15:04:05"), nil
}

func directScheduleDate(cfg config) (string, error) {
	day, err := time.ParseInLocation("2006-01-02", cfg.TargetDate, cfg.Timezone)
	if err != nil {
		return "", err
	}
	return day.Format("2006-01-02"), nil
}

func loadAuthToken(storagePath string) (string, error) {
	data, err := os.ReadFile(storagePath)
	if err != nil {
		return "", fmt.Errorf("read auth storage %s: %w", storagePath, err)
	}

	var state storageState
	if err := json.Unmarshal(data, &state); err != nil {
		return "", fmt.Errorf("decode auth storage: %w", err)
	}

	for _, origin := range state.Origins {
		for _, item := range origin.LocalStorage {
			if item.Name == "CpAuthToken" && strings.TrimSpace(item.Value) != "" {
				return item.Value, nil
			}
		}
	}
	for _, cookie := range state.Cookies {
		if cookie.Name == "CpAuthToken" && strings.TrimSpace(cookie.Value) != "" {
			return cookie.Value, nil
		}
	}
	return "", fmt.Errorf("CpAuthToken not found in %s; run headed login once first", storagePath)
}

func userIDFromAuthToken(token string) (int, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0, fmt.Errorf("auth token is not a JWT")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, fmt.Errorf("decode auth token payload: %w", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0, fmt.Errorf("decode auth token claims: %w", err)
	}

	value, ok := claims["UserId"]
	if !ok {
		return 0, fmt.Errorf("UserId not found in auth token")
	}
	switch typed := value.(type) {
	case string:
		userID, err := strconv.Atoi(typed)
		if err != nil {
			return 0, err
		}
		return userID, nil
	case float64:
		return int(typed), nil
	default:
		return 0, fmt.Errorf("unexpected UserId claim type %T", value)
	}
}

func clientPortalBaseURL(raw string) (string, error) {
	index := strings.Index(strings.ToLower(raw), "/clientportal2")
	if index < 0 {
		return "", fmt.Errorf("PERFECTGYM_BASE_URL must contain /clientportal2")
	}
	return raw[:index] + "/clientportal2", nil
}

func zoneTypeIDFromURL(raw string) (int, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return 0, fmt.Errorf("parse target url %q: %w", raw, err)
	}
	value := parsed.Query().Get("zoneTypeId")
	if value == "" {
		if match := regexp.MustCompile(`zoneTypeId=(\d+)`).FindStringSubmatch(raw); len(match) == 2 {
			value = match[1]
		}
	}
	if value == "" {
		return 0, fmt.Errorf("zoneTypeId not found in %q", raw)
	}
	zoneTypeID, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse zoneTypeId %q: %w", value, err)
	}
	return zoneTypeID, nil
}

func startNetworkCapture(context playwright.BrowserContext, capturePath string, bodyLimit int) (func(), error) {
	if bodyLimit < 0 {
		bodyLimit = 0
	}
	if err := os.MkdirAll(filepath.Dir(capturePath), 0o755); err != nil {
		return nil, fmt.Errorf("create network capture dir: %w", err)
	}

	file, err := os.Create(capturePath)
	if err != nil {
		return nil, fmt.Errorf("create network capture file: %w", err)
	}

	var mu sync.Mutex
	encoder := json.NewEncoder(file)
	writeRecord := func(record networkCaptureRecord) {
		mu.Lock()
		defer mu.Unlock()
		if err := encoder.Encode(record); err != nil {
			log.Printf("write network capture record: %v", err)
		}
	}

	context.OnRequest(func(request playwright.Request) {
		if !shouldCaptureRequest(request) {
			return
		}

		headers := request.Headers()
		postData, err := request.PostData()
		if err != nil {
			postData = ""
		}
		postData, postTruncated := truncateCaptureText(postData, bodyLimit)

		writeRecord(networkCaptureRecord{
			Type:          "request",
			At:            time.Now().Format(time.RFC3339Nano),
			Method:        request.Method(),
			URL:           request.URL(),
			ResourceType:  request.ResourceType(),
			Headers:       headers,
			PostData:      postData,
			BodyTruncated: postTruncated,
		})
	})

	context.OnResponse(func(response playwright.Response) {
		request := response.Request()
		if request == nil || !shouldCaptureRequest(request) {
			return
		}

		headers := response.Headers()

		writeRecord(networkCaptureRecord{
			Type:         "response",
			At:           time.Now().Format(time.RFC3339Nano),
			Method:       request.Method(),
			URL:          request.URL(),
			ResourceType: request.ResourceType(),
			Status:       response.Status(),
			Headers:      headers,
		})
	})

	context.OnRequestFailed(func(request playwright.Request) {
		if !shouldCaptureRequest(request) {
			return
		}
		errText := ""
		if err := request.Failure(); err != nil {
			errText = err.Error()
		}
		writeRecord(networkCaptureRecord{
			Type:         "request_failed",
			At:           time.Now().Format(time.RFC3339Nano),
			Method:       request.Method(),
			URL:          request.URL(),
			ResourceType: request.ResourceType(),
			Error:        errText,
		})
	})

	return func() {
		mu.Lock()
		defer mu.Unlock()
		if err := file.Close(); err != nil {
			log.Printf("close network capture file: %v", err)
		}
	}, nil
}

func shouldCaptureRequest(request playwright.Request) bool {
	url := strings.ToLower(request.URL())
	if !strings.Contains(url, "perfectgym") && !strings.Contains(url, "thekallang") {
		return false
	}
	if request.Method() != "GET" {
		return true
	}

	resourceType := request.ResourceType()
	return resourceType == "xhr" || resourceType == "fetch"
}

func truncateCaptureText(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	return value[:limit], true
}

func ensureLoggedIn(page playwright.Page, context playwright.BrowserContext, cfg config, storagePath string) error {
	if _, err := page.Goto(cfg.LoginURL); err != nil {
		return fmt.Errorf("open login page: %w", err)
	}
	time.Sleep(1500 * time.Millisecond)
	if err := dismissCommonPopups(page); err != nil {
		log.Printf("dismiss popup on login page: %v", err)
	}

	required, err := isLoginRequired(page)
	if err != nil {
		return fmt.Errorf("check login requirement: %w", err)
	}
	if !required {
		return nil
	}

	log.Println("Login is required.")

	autoLoggedIn, err := tryCredentialLogin(page, cfg)
	if err != nil {
		return err
	}
	if !autoLoggedIn {
		log.Println("Please finish login manually in the opened browser window.")
		if err := waitForManualLogin(page, cfg.ManualLoginWait); err != nil {
			return err
		}
	}

	if _, err := context.StorageState(storagePath); err != nil {
		return fmt.Errorf("persist storage state after login: %w", err)
	}
	return nil
}

func tryCredentialLogin(page playwright.Page, cfg config) (bool, error) {
	if cfg.Email == "" || cfg.Password == "" {
		return false, nil
	}

	emailInput := page.Locator(`input[type="email"], input[name*="email" i], input[autocomplete="username"]`).First()
	passwordInput := page.Locator(`input[type="password"]`).First()
	if !isVisible(emailInput) || !isVisible(passwordInput) {
		return false, nil
	}

	if err := emailInput.Fill(cfg.Email); err != nil {
		return false, fmt.Errorf("fill email: %w", err)
	}
	if err := passwordInput.Fill(cfg.Password); err != nil {
		return false, fmt.Errorf("fill password: %w", err)
	}

	submitButton, found := firstVisible(page, []string{
		`button:text-matches("sign in|log in|login","i")`,
		`[role="button"]:text-matches("sign in|log in|login","i")`,
	})
	if found {
		if err := submitButton.Click(); err != nil {
			return false, fmt.Errorf("submit login form: %w", err)
		}
	} else {
		if err := passwordInput.Press("Enter"); err != nil {
			return false, fmt.Errorf("submit login form with Enter: %w", err)
		}
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)
		required, err := isLoginRequired(page)
		if err == nil && !required {
			return true, nil
		}
	}

	return false, nil
}

func waitForManualLogin(page playwright.Page, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		required, err := isLoginRequired(page)
		if err == nil && !required {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("manual login did not finish within %s", timeout)
}

func isLoginRequired(page playwright.Page) (bool, error) {
	bodyText, err := page.Locator("body").InnerText()
	if err != nil {
		return false, err
	}
	if loginRequiredPattern.MatchString(bodyText) {
		return true, nil
	}

	passwordInput := page.Locator(`input[type="password"]`).First()
	return isVisible(passwordInput), nil
}

func openBookingPage(page playwright.Page, url string) error {
	if _, err := page.Goto(url); err != nil {
		return fmt.Errorf("open booking page: %w", err)
	}
	if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State:   playwright.LoadStateNetworkidle,
		Timeout: playwright.Float(20_000),
	}); err != nil {
		log.Printf("wait for booking page network idle: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := dismissCommonPopups(page); err != nil {
		log.Printf("dismiss popup on booking page: %v", err)
	}
	return nil
}

func prepareBookingTargets(context playwright.BrowserContext, seedPage playwright.Page, cfg config) ([]bookingTargetSession, error) {
	targets := make([]bookingTargetSession, 0, len(cfg.Targets))
	for index, target := range cfg.Targets {
		targetName := target.Name
		currentPage := seedPage
		if index > 0 {
			var err error
			currentPage, err = context.NewPage()
			if err != nil {
				return nil, fmt.Errorf("create page for %s: %w", target.Name, err)
			}
		}

		if err := openBookingPage(currentPage, target.URL); err != nil {
			return nil, fmt.Errorf("open %s booking page: %w", target.Name, err)
		}
		if err := navigateToTargetDate(currentPage, cfg.TargetDate, cfg.Timezone); err != nil {
			return nil, fmt.Errorf("navigate %s to target date: %w", target.Name, err)
		}
		currentPage.OnClose(func(playwright.Page) {
			log.Printf("%s booking page was closed.", targetName)
		})

		targets = append(targets, bookingTargetSession{
			config: target,
			page:   currentPage,
		})
	}
	return targets, nil
}

func navigateToTargetDate(page playwright.Page, targetDate string, location *time.Location) error {
	targetDay, err := time.ParseInLocation("2006-01-02", targetDate, location)
	if err != nil {
		return fmt.Errorf("parse target date %q: %w", targetDate, err)
	}

	targetLabel := formatDateLabel(targetDate, location)
	calendarLabel := formatCalendarDateLabel(targetDay)
	log.Printf("Navigating to target date: %s (%s, calendar header %s)", targetDate, targetLabel, calendarLabel)

	if err := waitForCalendarDates(page, 20*time.Second); err != nil {
		return err
	}

	for i := 0; i < 12; i++ {
		dates, err := visibleCalendarDateTexts(page)
		if err != nil {
			return err
		}
		if calendarContainsTargetDate(dates, targetDay) {
			log.Printf("Target date %s is visible in calendar headers: %s", targetDate, strings.Join(dates, ", "))
			return nil
		}

		direction := "next"
		firstDay, lastDay, ok := visibleCalendarDateRange(dates, targetDay, location)
		if ok {
			switch {
			case targetDay.Before(firstDay):
				direction = "previous"
			case targetDay.After(lastDay):
				direction = "next"
			default:
				return fmt.Errorf(
					"target date %s should be inside visible calendar range %s to %s, but header %s was not found; visible headers: %s",
					targetDate,
					firstDay.Format("2006-01-02"),
					lastDay.Format("2006-01-02"),
					calendarLabel,
					strings.Join(dates, ", "),
				)
			}
		}

		if err := clickCalendarWeekButton(page, direction); err != nil {
			return fmt.Errorf("navigate calendar %s toward %s from visible headers %s: %w", direction, targetDate, strings.Join(dates, ", "), err)
		}
		if err := waitForCalendarDateChange(page, dates, 10*time.Second); err != nil {
			return err
		}
	}

	dates, _ := visibleCalendarDateTexts(page)
	return fmt.Errorf("could not navigate calendar to target date %s after 12 week moves; visible headers: %s", targetDate, strings.Join(dates, ", "))
}

func waitForCalendarDates(page playwright.Page, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dates, err := visibleCalendarDateTexts(page)
		if err == nil && len(dates) > 0 {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}

	bodyText, _ := page.Locator("body").InnerText()
	return fmt.Errorf("calendar dates did not become visible within %s; page text: %s", timeout, shortLogText(bodyText, 500))
}

func waitForCalendarDateChange(page playwright.Page, previousDates []string, timeout time.Duration) error {
	previous := strings.Join(previousDates, "|")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dates, err := visibleCalendarDateTexts(page)
		if err == nil && len(dates) > 0 && strings.Join(dates, "|") != previous {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("calendar dates did not change within %s; still visible: %s", timeout, strings.Join(previousDates, ", "))
}

func visibleCalendarDateTexts(page playwright.Page) ([]string, error) {
	dates := page.Locator(".cp-calendar-date")
	count, err := dates.Count()
	if err != nil {
		return nil, fmt.Errorf("count calendar dates: %w", err)
	}

	var visibleDates []string
	for i := 0; i < count; i++ {
		date := dates.Nth(i)
		if !isVisible(date) {
			continue
		}
		text, err := date.InnerText()
		if err != nil {
			continue
		}
		text = strings.Join(strings.Fields(text), "")
		if !calendarDatePattern.MatchString(text) {
			continue
		}
		visibleDates = append(visibleDates, text)
	}
	return visibleDates, nil
}

func calendarContainsTargetDate(calendarDates []string, targetDay time.Time) bool {
	for _, raw := range calendarDates {
		day, ok := parseCalendarDateText(raw, targetDay, targetDay.Location())
		if ok && sameCalendarDay(day, targetDay) {
			return true
		}
	}
	return false
}

func visibleCalendarDateRange(calendarDates []string, targetDay time.Time, location *time.Location) (time.Time, time.Time, bool) {
	var parsedDates []time.Time
	for _, raw := range calendarDates {
		day, ok := parseCalendarDateText(raw, targetDay, location)
		if ok {
			parsedDates = append(parsedDates, day)
		}
	}
	if len(parsedDates) == 0 {
		return time.Time{}, time.Time{}, false
	}
	sort.Slice(parsedDates, func(i, j int) bool {
		return parsedDates[i].Before(parsedDates[j])
	})
	return parsedDates[0], parsedDates[len(parsedDates)-1], true
}

func parseCalendarDateText(raw string, targetDay time.Time, location *time.Location) (time.Time, bool) {
	match := calendarDatePattern.FindStringSubmatch(raw)
	if len(match) != 3 {
		return time.Time{}, false
	}

	day, err := strconv.Atoi(match[1])
	if err != nil {
		return time.Time{}, false
	}
	month, err := strconv.Atoi(match[2])
	if err != nil {
		return time.Time{}, false
	}
	if day < 1 || day > 31 || month < 1 || month > 12 {
		return time.Time{}, false
	}

	candidate := time.Date(targetDay.Year(), time.Month(month), day, 0, 0, 0, 0, location)
	if candidate.Sub(targetDay) > 183*24*time.Hour {
		candidate = candidate.AddDate(-1, 0, 0)
	}
	if targetDay.Sub(candidate) > 183*24*time.Hour {
		candidate = candidate.AddDate(1, 0, 0)
	}
	return candidate, true
}

func sameCalendarDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func formatCalendarDateLabel(day time.Time) string {
	return fmt.Sprintf("%d/%d", day.Day(), int(day.Month()))
}

func clickCalendarWeekButton(page playwright.Page, direction string) error {
	label := "Next week"
	if direction == "previous" {
		label = "Previous week"
	}

	button, found := firstVisible(page, []string{
		fmt.Sprintf(`[aria-label="%s"]`, label),
		fmt.Sprintf(`[name="%s"]`, label),
		fmt.Sprintf(`[alt="%s"]`, label),
	})
	if !found {
		return fmt.Errorf("%s button is not visible", label)
	}
	if err := button.Click(); err != nil {
		return fmt.Errorf("click %s: %w", label, err)
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}

func shortLogText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func pollForTargetSlot(targets []bookingTargetSession, cfg config) (*slotMatch, error) {
	attempts := cfg.RefreshAttempts
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		log.Printf("Refresh burst %d/%d", attempt, attempts)
		for _, target := range targets {
			if target.page.IsClosed() {
				return nil, fmt.Errorf("%s booking page was closed before polling completed", target.config.Name)
			}
			if err := dismissCommonPopups(target.page); err != nil {
				log.Printf("dismiss popup while polling %s: %v", target.config.Name, err)
			}
			if err := navigateToTargetDate(target.page, cfg.TargetDate, cfg.Timezone); err != nil {
				log.Printf("navigate %s to target date while polling: %v", target.config.Name, err)
				continue
			}

			slot, err := findMatchingSlot(target.page, target.config, cfg)
			if err != nil {
				log.Printf("scan %s slot candidates: %v", target.config.Name, err)
			}
			if slot != nil {
				log.Printf("Found matching slot on %s.", target.config.Name)
				return slot, nil
			}

			if attempt < attempts {
				if err := tryRefreshAvailability(target.page); err != nil {
					log.Printf("refresh availability for %s: %v", target.config.Name, err)
				}
			}
		}

		if attempt < attempts {
			time.Sleep(cfg.RefreshSpacing)
		}
	}

	return nil, nil
}

func findMatchingSlot(page playwright.Page, target bookingTargetConfig, cfg config) (*slotMatch, error) {
	if slot, err := findCalendarMatchingSlot(page, target, cfg); err != nil {
		return nil, err
	} else if slot != nil {
		return slot, nil
	}

	candidateSelectors := []string{
		`.cp-calendar-item.is-bookable`,
		`[class*="slot"]`,
		`[class*="booking"]`,
		`[class*="facility"]`,
		`[class*="resource"]`,
		`button:has-text("Book")`,
		`button:has-text("Add")`,
	}

	var best *scoredSlot
	ordinal := 0
	for _, selector := range candidateSelectors {
		candidates := page.Locator(selector)
		count, err := candidates.Count()
		if err != nil {
			continue
		}

		for i := 0; i < count; i++ {
			candidate := candidates.Nth(i)
			text, err := candidate.InnerText()
			if err != nil {
				continue
			}
			text = strings.ToLower(strings.TrimSpace(text))
			if text == "" {
				continue
			}
			if target.CourtKeyword != "" && !strings.Contains(text, target.CourtKeyword) {
				continue
			}
			if cfg.TargetCourtKeyword != "" && !strings.Contains(text, cfg.TargetCourtKeyword) {
				continue
			}
			if !targetTimeMatchesText(text, cfg.TargetTimeText) {
				continue
			}

			action, found := findAddToCartAction(candidate)
			if !found {
				ordinal++
				continue
			}

			courtRank := 0
			if len(target.CourtPriorities) > 0 {
				rank, matched := courtPriorityRank(text, target.CourtPriorities, target.CourtType)
				if !matched {
					ordinal++
					continue
				}
				courtRank = rank
			}

			durationRank := durationPriorityRank(text, cfg.PreferredDurations)
			if durationRank >= len(cfg.PreferredDurations) {
				ordinal++
				continue
			}

			current := &scoredSlot{
				slot: &slotMatch{
					page:      page,
					candidate: candidate,
					action:    action,
					text:      text,
					target:    target.Name,
				},
				courtRank:    courtRank,
				durationRank: durationRank,
				ordinal:      ordinal,
			}
			if best == nil || isBetterSlot(current, best) {
				best = current
			}
			ordinal++
		}
	}

	if best == nil {
		return nil, nil
	}

	log.Printf("Selected %s slot candidate: %s", target.Name, best.slot.text)
	return best.slot, nil
}

func findCalendarMatchingSlot(page playwright.Page, target bookingTargetConfig, cfg config) (*slotMatch, error) {
	targetDay, err := time.ParseInLocation("2006-01-02", cfg.TargetDate, cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("parse target date for calendar slot scan: %w", err)
	}

	targetDayIndex, headers, err := targetCalendarDayIndex(page, targetDay)
	if err != nil {
		return nil, err
	}
	if targetDayIndex < 0 {
		return nil, fmt.Errorf("target date %s is not visible while scanning calendar slots; visible headers: %s", cfg.TargetDate, strings.Join(headers, ", "))
	}

	items := page.Locator(`.cp-calendar-item.is-bookable`)
	count, err := items.Count()
	if err != nil {
		return nil, fmt.Errorf("count calendar bookable items: %w", err)
	}

	var best *scoredSlot
	ordinal := 0
	for i := 0; i < count; i++ {
		item := items.Nth(i)
		if !isVisible(item) {
			continue
		}

		itemDayIndex, ok := calendarItemDayIndex(item)
		if !ok || itemDayIndex != targetDayIndex {
			continue
		}

		text, err := item.InnerText()
		if err != nil {
			continue
		}
		text = strings.ToLower(strings.Join(strings.Fields(text), " "))
		if text == "" || !targetTimeMatchesText(text, cfg.TargetTimeText) {
			continue
		}

		action, found := findAddToCartAction(item)
		if !found {
			ordinal++
			continue
		}

		courtRank := 0
		if len(target.CourtPriorities) > 0 {
			rank, matched := courtPriorityRank(text, target.CourtPriorities, target.CourtType)
			if matched {
				courtRank = rank
			} else {
				courtRank = len(target.CourtPriorities)
			}
		}

		durationRank := durationPriorityRank(text, cfg.PreferredDurations)
		if durationRank >= len(cfg.PreferredDurations) {
			ordinal++
			continue
		}

		current := &scoredSlot{
			slot: &slotMatch{
				page:      page,
				candidate: item,
				action:    action,
				text:      fmt.Sprintf("%s %s", headers[targetDayIndex], text),
				target:    target.Name,
			},
			courtRank:    courtRank,
			durationRank: durationRank,
			ordinal:      ordinal,
		}
		if best == nil || isBetterSlot(current, best) {
			best = current
		}
		ordinal++
	}

	if best == nil {
		return nil, nil
	}

	log.Printf("Selected %s calendar slot candidate: %s", target.Name, best.slot.text)
	return best.slot, nil
}

func targetCalendarDayIndex(page playwright.Page, targetDay time.Time) (int, []string, error) {
	rawDates, err := visibleCalendarDateTexts(page)
	if err != nil {
		return -1, nil, err
	}

	headers := uniqueStrings(rawDates)
	for index, raw := range headers {
		day, ok := parseCalendarDateText(raw, targetDay, targetDay.Location())
		if ok && sameCalendarDay(day, targetDay) {
			return index, headers, nil
		}
	}
	return -1, headers, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func calendarItemDayIndex(item playwright.Locator) (int, bool) {
	value, err := item.Evaluate(`(el) => {
		const dayCell = el.closest('td.cp-calendar-day-col');
		const row = el.closest('tr');
		if (!dayCell || !row) return -1;
		return Array.from(row.querySelectorAll('td.cp-calendar-day-col')).indexOf(dayCell);
	}`, nil)
	if err != nil {
		return -1, false
	}

	switch typed := value.(type) {
	case int:
		return typed, typed >= 0
	case int64:
		return int(typed), typed >= 0
	case float64:
		return int(typed), typed >= 0
	default:
		return -1, false
	}
}

func findAddToCartAction(candidate playwright.Locator) (playwright.Locator, bool) {
	buttonSelectors := []string{
		`.cp-btn-classes-action`,
		`[ng-click="$ctrl.cta()"]`,
		`button:text-matches("add to cart|book|select","i")`,
		`[role="button"]:text-matches("add to cart|book|select","i")`,
	}

	for _, selector := range buttonSelectors {
		button := candidate.Locator(selector).First()
		if isVisible(button) {
			return button, true
		}
	}

	if isVisible(candidate) {
		return candidate, true
	}

	return nil, false
}

func addSlotToCart(slot *slotMatch, cfg config) error {
	log.Printf("Trying to add %s slot to cart: %s", slot.target, slot.text)
	if err := slot.action.Click(playwright.LocatorClickOptions{
		Force: playwright.Bool(true),
	}); err != nil {
		return fmt.Errorf("click slot action: %w", err)
	}
	time.Sleep(1 * time.Second)

	if err := choosePreferredDuration(slot.page, cfg.PreferredDurations); err != nil {
		log.Printf("choose booking duration: %v", err)
	}

	if err := dismissCommonPopups(slot.page); err != nil {
		log.Printf("dismiss popup after add-to-cart click: %v", err)
	}

	confirmSelectors := []string{
		`button:text-matches("add to cart|continue|confirm|book","i")`,
		`[role="button"]:text-matches("add to cart|continue|confirm|book","i")`,
	}
	for _, selector := range confirmSelectors {
		button := slot.page.Locator(selector).First()
		if !isVisible(button) {
			continue
		}
		if err := button.Click(); err != nil {
			log.Printf("click confirm button %q: %v", selector, err)
			continue
		}
		time.Sleep(500 * time.Millisecond)
	}

	return nil
}

func tryRefreshAvailability(page playwright.Page) error {
	refreshButton, found := firstVisible(page, []string{
		`button:text-matches("refresh|reload|search|update","i")`,
		`[role="button"]:text-matches("refresh|reload|search|update","i")`,
	})
	if found {
		if err := refreshButton.Click(); err != nil {
			return fmt.Errorf("click refresh button: %w", err)
		}
		return nil
	}

	if _, err := page.Reload(); err != nil {
		return fmt.Errorf("reload page: %w", err)
	}
	return nil
}

func dismissCommonPopups(page playwright.Page) error {
	for _, selector := range []string{
		`button:text-matches("^accept$|^ok$|^close$|^got it$","i")`,
		`[role="button"]:text-matches("^accept$|^ok$|^close$|^got it$","i")`,
	} {
		button := page.Locator(selector).First()
		if !isVisible(button) {
			continue
		}
		if err := button.Click(); err != nil {
			log.Printf("click popup button %q: %v", selector, err)
		}
	}
	return nil
}

func firstVisible(page playwright.Page, selectors []string) (playwright.Locator, bool) {
	for _, selector := range selectors {
		locator := page.Locator(selector).First()
		if isVisible(locator) {
			return locator, true
		}
	}
	return nil, false
}

func isVisible(locator playwright.Locator) bool {
	if locator == nil {
		return false
	}
	visible, err := locator.IsVisible()
	return err == nil && visible
}

func waitUntil(at time.Time, label string) {
	now := time.Now()
	if !at.After(now) {
		return
	}

	waitFor := time.Until(at)
	log.Printf("Waiting %s until %s at %s", waitFor.Round(time.Millisecond), label, at.Format(time.RFC3339Nano))
	time.Sleep(waitFor)
}

func requireEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("missing required environment variable: %s", name)
	}
	return value
}

func resolveTargetDate(location *time.Location) (string, error) {
	explicitDate := strings.TrimSpace(os.Getenv("TARGET_DATE"))
	if explicitDate != "" {
		if _, err := time.ParseInLocation("2006-01-02", explicitDate, location); err != nil {
			return "", fmt.Errorf("parse TARGET_DATE %q: %w", explicitDate, err)
		}
		return explicitDate, nil
	}

	offset := parseInt("TARGET_OFFSET_DAYS", defaultTargetOffset)
	date := time.Now().In(location).AddDate(0, 0, offset)
	return date.Format("2006-01-02"), nil
}

func resolveBookingStart(targetDate, targetTimeText string, location *time.Location) (*time.Time, error) {
	explicitStart := strings.TrimSpace(os.Getenv("BOOKING_START_AT"))
	if explicitStart != "" {
		return parseScheduledStart(explicitStart, location)
	}

	if !parseBool("AUTO_BOOKING_WINDOW", true) {
		return nil, nil
	}
	if strings.TrimSpace(targetTimeText) == "" {
		return nil, nil
	}

	targetDay, err := time.ParseInLocation("2006-01-02", targetDate, location)
	if err != nil {
		return nil, fmt.Errorf("parse target date for booking window: %w", err)
	}

	slotTime, err := parseTimeOfDay(targetTimeText)
	if err != nil {
		return nil, err
	}

	openLeadDays := parseInt("BOOKING_OPEN_LEAD_DAYS", defaultBookingLeadDays)
	startAt := time.Date(
		targetDay.Year(),
		targetDay.Month(),
		targetDay.Day(),
		slotTime.Hour(),
		slotTime.Minute(),
		0,
		0,
		location,
	).AddDate(0, 0, -openLeadDays)
	return &startAt, nil
}

func parseScheduledStart(value string, location *time.Location) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04",
	}
	for _, layout := range layouts {
		var (
			t   time.Time
			err error
		)
		if layout == time.RFC3339 {
			t, err = time.Parse(layout, value)
		} else {
			t, err = time.ParseInLocation(layout, value, location)
		}
		if err == nil {
			return &t, nil
		}
	}

	return nil, fmt.Errorf("parse BOOKING_START_AT %q: use RFC3339 or 2006-01-02 15:04:05", value)
}

func parseCourtPriorities(raw string) ([]courtPriority, error) {
	tokens := strings.FieldsFunc(strings.ReplaceAll(raw, ">", ","), func(r rune) bool {
		return r == ',' || r == ';'
	})
	if len(tokens) == 0 {
		return nil, nil
	}

	rangePattern := regexp.MustCompile(`^(\d+)\s*-\s*(\d+)$`)
	singlePattern := regexp.MustCompile(`^\d+$`)

	var priorities []courtPriority
	for _, token := range tokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			continue
		}

		if rangeMatch := rangePattern.FindStringSubmatch(token); len(rangeMatch) == 3 {
			start, _ := strconv.Atoi(rangeMatch[1])
			end, _ := strconv.Atoi(rangeMatch[2])
			step := 1
			if start > end {
				step = -1
			}
			for courtNumber := start; ; courtNumber += step {
				priorities = append(priorities, courtPriority{
					kind:   "outdoor",
					number: courtNumber,
					label:  fmt.Sprintf("outdoor %d", courtNumber),
				})
				if courtNumber == end {
					break
				}
			}
			continue
		}

		if singlePattern.MatchString(token) {
			courtNumber, _ := strconv.Atoi(token)
			priorities = append(priorities, courtPriority{
				kind:   "outdoor",
				number: courtNumber,
				label:  fmt.Sprintf("outdoor %d", courtNumber),
			})
			continue
		}

		return nil, fmt.Errorf("invalid OUTDOOR_COURT_PRIORITY token %q", token)
	}

	return priorities, nil
}

func parseDurationPriority(raw string) ([]int, error) {
	tokens := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '>'
	})
	if len(tokens) == 0 {
		return []int{120, 60}, nil
	}

	var durations []int
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}

		duration, err := strconv.Atoi(token)
		if err != nil {
			return nil, fmt.Errorf("invalid BOOKING_DURATION_PRIORITY token %q", token)
		}
		durations = append(durations, duration)
	}
	if len(durations) == 0 {
		return []int{120, 60}, nil
	}
	return durations, nil
}

func parseTimeOfDay(value string) (time.Time, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	layouts := []string{
		"15:04",
		"15:04:05",
		"3:04pm",
		"3pm",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse TARGET_TIME_TEXT %q: use values like 08:00 or 20:00", value)
}

func withZoneTypeID(raw string, zoneTypeID int) string {
	pattern := regexp.MustCompile(`zoneTypeId=\d+`)
	replacement := fmt.Sprintf("zoneTypeId=%d", zoneTypeID)
	if pattern.MatchString(raw) {
		return pattern.ReplaceAllString(raw, replacement)
	}

	separator := "?"
	if strings.Contains(raw, "?") {
		separator = "&"
	}
	return raw + separator + replacement
}

func courtPriorityRank(text string, priorities []courtPriority, defaultType string) (int, bool) {
	courtType, courtNumber := classifyCourt(text, defaultType)
	for index, priority := range priorities {
		if courtType != priority.kind {
			continue
		}
		if priority.number != 0 && courtNumber != priority.number {
			continue
		}
		return index, true
	}
	return len(priorities), false
}

func classifyCourt(text string, defaultType string) (string, int) {
	switch {
	case strings.Contains(text, "indoor"):
		return "indoor", extractCourtNumber(text)
	case strings.Contains(text, "outdoor"):
		return "outdoor", extractCourtNumber(text)
	default:
		return defaultType, extractCourtNumber(text)
	}
}

func extractCourtNumber(text string) int {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)outdoor(?:\s+court)?\s*#?\s*(\d+)`),
		regexp.MustCompile(`(?i)court\s*#?\s*(\d+)`),
	}
	for _, pattern := range patterns {
		match := pattern.FindStringSubmatch(text)
		if len(match) != 2 {
			continue
		}
		number, err := strconv.Atoi(match[1])
		if err == nil {
			return number
		}
	}
	return 0
}

func durationPriorityRank(text string, preferredDurations []int) int {
	for index, duration := range preferredDurations {
		if textMatchesDuration(text, duration) {
			return index
		}
	}
	return len(preferredDurations)
}

func textMatchesDuration(text string, duration int) bool {
	text = strings.ToLower(text)
	switch duration {
	case 120:
		return strings.Contains(text, "120") || strings.Contains(text, "2h") || strings.Contains(text, "2 hr") || strings.Contains(text, "2 hour")
	case 60:
		return strings.Contains(text, "60") || strings.Contains(text, "1h") || strings.Contains(text, "1 hr") || strings.Contains(text, "1 hour")
	default:
		return strings.Contains(text, strconv.Itoa(duration))
	}
}

func targetTimeMatchesText(text, targetTimeText string) bool {
	targetTimeText = strings.TrimSpace(strings.ToLower(targetTimeText))
	if targetTimeText == "" {
		return true
	}

	parsed, err := parseTimeOfDay(targetTimeText)
	if err != nil {
		return strings.Contains(strings.ToLower(text), targetTimeText)
	}

	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	compactText := strings.ReplaceAll(text, " ", "")
	for _, variant := range targetTimeTextExplicitPeriodVariants(parsed) {
		if strings.Contains(compactText, strings.ReplaceAll(variant, " ", "")) {
			return true
		}
	}

	for _, variant := range targetTimeTextNumericVariants(parsed) {
		if numericTimeVariantMatches(text, variant, parsed) {
			return true
		}
	}
	return false
}

func targetTimeTextExplicitPeriodVariants(value time.Time) []string {
	raw := []string{
		value.Format("3:04 PM"),
		value.Format("03:04 PM"),
		value.Format("3 PM"),
		value.Format("03 PM"),
	}

	var variants []string
	seen := make(map[string]bool, len(raw)*2)
	for _, item := range raw {
		for _, variant := range []string{
			strings.ToLower(item),
			strings.ReplaceAll(strings.ToLower(item), " ", ""),
		} {
			if seen[variant] {
				continue
			}
			seen[variant] = true
			variants = append(variants, variant)
		}
	}
	return variants
}

func targetTimeTextNumericVariants(value time.Time) []string {
	raw := []string{
		value.Format("15:04"),
		value.Format("15:04:05"),
	}
	if value.Hour() < 10 {
		raw = append(raw, value.Format("3:04"))
	}

	var variants []string
	seen := make(map[string]bool, len(raw))
	for _, item := range raw {
		variant := strings.ToLower(item)
		if seen[variant] {
			continue
		}
		seen[variant] = true
		variants = append(variants, variant)
	}
	return variants
}

func numericTimeVariantMatches(text, variant string, target time.Time) bool {
	pattern := regexp.MustCompile(`(^|[^0-9])` + regexp.QuoteMeta(variant) + `([^0-9]|$)`)
	matches := pattern.FindAllStringSubmatchIndex(text, -1)
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		end := match[4]
		suffix := strings.TrimLeft(text[end:], " \t\n\r")
		switch {
		case strings.HasPrefix(suffix, "am"):
			if target.Hour() < 12 {
				return true
			}
		case strings.HasPrefix(suffix, "pm"):
			if target.Hour() >= 12 {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func isBetterSlot(current, best *scoredSlot) bool {
	if current.courtRank != best.courtRank {
		return current.courtRank < best.courtRank
	}
	if current.durationRank != best.durationRank {
		return current.durationRank < best.durationRank
	}
	return current.ordinal < best.ordinal
}

func choosePreferredDuration(page playwright.Page, preferredDurations []int) error {
	for _, duration := range preferredDurations {
		for _, selector := range durationSelectors(duration) {
			option := page.Locator(selector).First()
			if !isVisible(option) {
				continue
			}
			if err := option.Click(); err != nil {
				log.Printf("click duration option %q: %v", selector, err)
				continue
			}
			log.Printf("Selected %d-minute booking option.", duration)
			time.Sleep(300 * time.Millisecond)
			return nil
		}
	}
	return nil
}

func durationSelectors(duration int) []string {
	switch duration {
	case 120:
		return []string{
			`button:text-matches("^120$|120\s*(min|mins|minutes)|2\s*(h|hr|hrs|hours?)$","i")`,
			`[role="button"]:text-matches("^120$|120\s*(min|mins|minutes)|2\s*(h|hr|hrs|hours?)$","i")`,
			`label:text-matches("^120$|120\s*(min|mins|minutes)|2\s*(h|hr|hrs|hours?)$","i")`,
			`text=/\b120\s*(min|mins|minutes)?\b/i`,
			`text=/\b2\s*(h|hr|hrs|hours?)\b/i`,
		}
	case 60:
		return []string{
			`button:text-matches("^60$|60\s*(min|mins|minutes)|1\s*(h|hr|hrs|hours?)$","i")`,
			`[role="button"]:text-matches("^60$|60\s*(min|mins|minutes)|1\s*(h|hr|hrs|hours?)$","i")`,
			`label:text-matches("^60$|60\s*(min|mins|minutes)|1\s*(h|hr|hrs|hours?)$","i")`,
			`text=/\b60\s*(min|mins|minutes)?\b/i`,
			`text=/\b1\s*(h|hr|hrs|hours?)\b/i`,
		}
	default:
		pattern := strconv.Itoa(duration)
		return []string{
			fmt.Sprintf(`button:text-matches("^%s$|%s\s*(min|mins|minutes)$","i")`, pattern, pattern),
			fmt.Sprintf(`[role="button"]:text-matches("^%s$|%s\s*(min|mins|minutes)$","i")`, pattern, pattern),
			fmt.Sprintf(`text=/\b%s\b/i`, pattern),
		}
	}
}

func parseBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	switch strings.ToLower(value) {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		return fallback
	}
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

func parseDurationMilliseconds(name string, fallback time.Duration) time.Duration {
	return time.Duration(parseInt(name, int(fallback/time.Millisecond))) * time.Millisecond
}

func parseDurationSeconds(name string, fallback time.Duration) time.Duration {
	return time.Duration(parseInt(name, int(fallback/time.Second))) * time.Second
}

func formatDateLabel(dateText string, location *time.Location) string {
	date, err := time.ParseInLocation("2006-01-02", dateText, location)
	if err != nil {
		return dateText
	}

	return date.Format("Mon, 2 Jan 2006")
}

func getEnvDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
