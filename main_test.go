package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testTimeLayout = "2006-01-02T15:04:05.000Z"

func mustParseTime(layout, value string) time.Time {
	t, err := time.Parse(layout, value)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse time '%s' with layout '%s': %v", value, layout, err))
	}
	return t
}

func createTempConfigFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "config.json")
	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}
	return filePath
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      time.Duration
		expectErr bool
	}{
		{"Valid Full", "01:02:03.456", time.Hour + 2*time.Minute + 3*time.Second + 456*time.Millisecond, false},
		{"Valid No Millis", "00:10:30", 10*time.Minute + 30*time.Second, false},
		{"Valid Short Millis 1", "00:00:01.5", 1*time.Second + 500*time.Millisecond, false},
		{"Valid Short Millis 2", "00:00:02.05", 2*time.Second + 50*time.Millisecond, false},
		{"Zero Duration", "00:00:00.000", 0, false},
		{"Invalid Format Colon", "01-02-03.456", 0, true},
		{"Invalid Format Parts", "01:02", 0, true},
		{"Invalid Format Too Many Parts", "01:02:03:04", 0, true},
		{"Invalid Hour", "xx:02:03.456", 0, true},
		{"Invalid Minute", "01:xx:03.456", 0, true},
		{"Invalid Second", "01:02:xx.456", 0, true},
		{"Invalid Millis", "01:02:03.xxx", 0, true},
		{"Empty String", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDuration(tt.input)

			if (err != nil) != tt.expectErr {
				t.Errorf("parseDuration(%q) error = %v, expectErr %v", tt.input, err, tt.expectErr)
				return
			}
			if !tt.expectErr && got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name  string
		input time.Duration
		want  string
	}{
		{"Zero", 0, "00:00:00.000"},
		{"Millis Only", 123 * time.Millisecond, "00:00:00.123"},
		{"Seconds Only", 45 * time.Second, "00:00:45.000"},
		{"Minutes Only", 15 * time.Minute, "00:15:00.000"},
		{"Hours Only", 2 * time.Hour, "02:00:00.000"},
		{"Full", 1*time.Hour + 23*time.Minute + 45*time.Second + 678*time.Millisecond, "01:23:45.678"},
		{"Short Millis", 5*time.Second + 50*time.Millisecond, "00:00:05.050"}, // Needs padding
		{"Long Duration", 25*time.Hour + 1*time.Minute + 1*time.Second + 1*time.Millisecond, "25:01:01.001"},
		{"Negative Duration", -(1*time.Minute + 30*time.Second), "00:01:30.000"}, // Should format as positive
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDuration(tt.input); got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	validConfigContent := `{
		"laps": 3,
		"lapLen": 1500.0,
		"penaltyLen": 150.0,
		"firingLines": 10,
		"start": "12:00:00",
		"startDelta": "00:00:30.000"
	}`
	expectedStartTime, _ := time.Parse(configTimeLayout, "12:00:00")
	expectedStartDelta, _ := parseDuration("00:00:30.000")

	expectedConfig := &Config{
		Laps:             3,
		LapLen:           1500.0,
		PenaltyLen:       150.0,
		FiringLines:      10,
		Start:            "12:00:00",
		StartDelta:       "00:00:30.000",
		parsedStart:      expectedStartTime,
		parsedStartDelta: expectedStartDelta,
	}

	tests := []struct {
		name        string
		setup       func(t *testing.T) string
		want        *Config
		wantErrStr  string
		checkParsed bool
	}{
		{
			name: "Valid Config",
			setup: func(t *testing.T) string {
				return createTempConfigFile(t, validConfigContent)
			},
			want:        expectedConfig,
			wantErrStr:  "",
			checkParsed: true,
		},
		{
			name: "File Not Found",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nonexistent.json")
			},
			want:       nil,
			wantErrStr: "error opening config file:",
		},
		{
			name: "Invalid JSON",
			setup: func(t *testing.T) string {
				return createTempConfigFile(t, `{"laps": 3, "lapLen": 1500.0,`)
			},
			want:       nil,
			wantErrStr: "error parsing config JSON:",
		},
		{
			name: "Invalid Start Time Format",
			setup: func(t *testing.T) string {
				invalidTimeContent := strings.Replace(validConfigContent, `"12:00:00"`, `"12-00-00"`, 1)
				return createTempConfigFile(t, invalidTimeContent)
			},
			want:       nil,
			wantErrStr: "error parsing config start time:",
		},
		{
			name: "Invalid Start Delta Format",
			setup: func(t *testing.T) string {
				invalidDeltaContent := strings.Replace(validConfigContent, `"00:00:30.000"`, `"invalid"`, 1)
				return createTempConfigFile(t, invalidDeltaContent)
			},
			want:       nil,
			wantErrStr: "error parsing config start delta 'invalid':",
		},
		{
			name: "Missing Field (Laps)",
			setup: func(t *testing.T) string {
				missingFieldContent := `{
					"lapLen": 1500.0,
					"penaltyLen": 150.0,
					"firingLines": 10,
					"start": "12:00:00",
					"startDelta": "00:00:30.000"
				}`
				return createTempConfigFile(t, missingFieldContent)
			},
			want: &Config{
				Laps:             0, // Zero value
				LapLen:           1500.0,
				PenaltyLen:       150.0,
				FiringLines:      10,
				Start:            "12:00:00",
				StartDelta:       "00:00:30.000",
				parsedStart:      expectedStartTime,
				parsedStartDelta: expectedStartDelta,
			},
			wantErrStr:  "",
			checkParsed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := tt.setup(t)
			got, err := loadConfig(configPath)

			if tt.wantErrStr != "" {
				if err == nil {
					t.Errorf("loadConfig() expected error containing %q, but got nil", tt.wantErrStr)
				} else if !strings.Contains(err.Error(), tt.wantErrStr) {
					t.Errorf("loadConfig() error = %v, want error containing %q", err, tt.wantErrStr)
				}
			} else {
				if err != nil {
					t.Errorf("loadConfig() unexpected error = %v", err)
				}
				if got == nil && tt.want != nil {
					t.Errorf("loadConfig() got nil, want non-nil")
					return
				}
				if got != nil && tt.want == nil {
					t.Errorf("loadConfig() got non-nil, want nil")
					return
				}
				if got != nil && tt.want != nil {
					if got.Laps != tt.want.Laps || got.LapLen != tt.want.LapLen ||
						got.PenaltyLen != tt.want.PenaltyLen || got.FiringLines != tt.want.FiringLines ||
						got.Start != tt.want.Start || got.StartDelta != tt.want.StartDelta {
						t.Errorf("loadConfig() basic fields mismatch. Got %+v, want %+v", got, tt.want)
					}
					if tt.checkParsed {
						if !got.parsedStart.Equal(tt.want.parsedStart) {
							t.Errorf("loadConfig() parsedStart mismatch. Got %v, want %v", got.parsedStart, tt.want.parsedStart)
						}
						if got.parsedStartDelta != tt.want.parsedStartDelta {
							t.Errorf("loadConfig() parsedStartDelta mismatch. Got %v, want %v", got.parsedStartDelta, tt.want.parsedStartDelta)
						}
					}
				}
			}
		})
	}
}

func TestLap_Duration(t *testing.T) {
	t1 := mustParseTime(testTimeLayout, "2023-10-26T10:00:00.000Z")
	t2 := mustParseTime(testTimeLayout, "2023-10-26T10:05:30.500Z")

	tests := []struct {
		name string
		lap  Lap
		want time.Duration
	}{
		{"Valid Duration", Lap{StartTime: t1, EndTime: t2}, 5*time.Minute + 30*time.Second + 500*time.Millisecond},
		{"Zero Start Time", Lap{StartTime: time.Time{}, EndTime: t2}, 0},
		{"Zero End Time", Lap{StartTime: t1, EndTime: time.Time{}}, 0},
		{"Zero Both Times", Lap{StartTime: time.Time{}, EndTime: time.Time{}}, 0},
		{"Same Start/End Time", Lap{StartTime: t1, EndTime: t1}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lap.Duration(); got != tt.want {
				t.Errorf("Lap.Duration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLap_AverageSpeed(t *testing.T) {
	t1 := mustParseTime(testTimeLayout, "2023-10-26T10:00:00.000Z")
	t2 := mustParseTime(testTimeLayout, "2023-10-26T10:02:00.000Z")
	const tolerance = 1e-9

	tests := []struct {
		name string
		lap  Lap
		want float64
	}{
		{"Valid Speed", Lap{StartTime: t1, EndTime: t2, Distance: 1500.0}, 1500.0 / 120.0},
		{"Zero Distance", Lap{StartTime: t1, EndTime: t2, Distance: 0.0}, 0.0},
		{"Zero Duration", Lap{StartTime: t1, EndTime: t1, Distance: 1500.0}, 0.0},
		{"Zero Start Time", Lap{StartTime: time.Time{}, EndTime: t2, Distance: 1500.0}, 0.0},
		{"Zero End Time", Lap{StartTime: t1, EndTime: time.Time{}, Distance: 1500.0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.lap.AverageSpeed()
			if math.Abs(got-tt.want) > tolerance {
				t.Errorf("Lap.AverageSpeed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPenaltyLap_Duration(t *testing.T) {
	t1 := mustParseTime(testTimeLayout, "2023-10-26T10:10:00.000Z")
	t2 := mustParseTime(testTimeLayout, "2023-10-26T10:10:45.250Z")

	tests := []struct {
		name string
		lap  PenaltyLap
		want time.Duration
	}{
		{"Valid Duration", PenaltyLap{StartTime: t1, EndTime: t2}, 45*time.Second + 250*time.Millisecond},
		{"Zero Start Time", PenaltyLap{StartTime: time.Time{}, EndTime: t2}, 0},
		{"Zero End Time", PenaltyLap{StartTime: t1, EndTime: time.Time{}}, 0},
		{"Zero Both Times", PenaltyLap{StartTime: time.Time{}, EndTime: time.Time{}}, 0},
		{"Same Start/End Time", PenaltyLap{StartTime: t1, EndTime: t1}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lap.Duration(); got != tt.want {
				t.Errorf("PenaltyLap.Duration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPenaltyLap_AverageSpeed(t *testing.T) {
	t1 := mustParseTime(testTimeLayout, "2023-10-26T10:10:00.000Z")
	t2 := mustParseTime(testTimeLayout, "2023-10-26T10:10:30.000Z")
	const tolerance = 1e-9

	tests := []struct {
		name string
		lap  PenaltyLap
		want float64
	}{
		{"Valid Speed", PenaltyLap{StartTime: t1, EndTime: t2, Distance: 150.0}, 150.0 / 30.0},
		{"Zero Distance", PenaltyLap{StartTime: t1, EndTime: t2, Distance: 0.0}, 0.0},
		{"Zero Duration", PenaltyLap{StartTime: t1, EndTime: t1, Distance: 150.0}, 0.0},
		{"Zero Start Time", PenaltyLap{StartTime: time.Time{}, EndTime: t2, Distance: 150.0}, 0.0},
		{"Zero End Time", PenaltyLap{StartTime: t1, EndTime: time.Time{}, Distance: 150.0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.lap.AverageSpeed()
			if math.Abs(got-tt.want) > tolerance {
				t.Errorf("PenaltyLap.AverageSpeed() = %v, want %v", got, tt.want)
			}
		})
	}
}
