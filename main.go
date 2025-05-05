package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const timeLayout = "15:04:05.000" // HH:MM:SS.sss
const eventTimeLayout = "[" + timeLayout + "]"
const configTimeLayout = "15:04:05"

type Config struct {
	Laps        int     `json:"laps"`
	LapLen      float64 `json:"lapLen"`
	PenaltyLen  float64 `json:"penaltyLen"`
	FiringLines int     `json:"firingLines"`
	Start       string  `json:"start"`
	StartDelta  string  `json:"startDelta"`

	parsedStart      time.Time
	parsedStartDelta time.Duration
}

type Lap struct {
	Number    int
	StartTime time.Time
	EndTime   time.Time
	Distance  float64
}

func (l Lap) Duration() time.Duration {
	if l.StartTime.IsZero() || l.EndTime.IsZero() {
		return 0
	}
	return l.EndTime.Sub(l.StartTime)
}

func (l Lap) AverageSpeed() float64 {
	durationSeconds := l.Duration().Seconds()
	if durationSeconds <= 0 || l.Distance <= 0 {
		return 0.0
	}
	return l.Distance / durationSeconds
}

type PenaltyLap struct {
	StartTime time.Time
	EndTime   time.Time
	Distance  float64
}

func (p PenaltyLap) Duration() time.Duration {
	if p.StartTime.IsZero() || p.EndTime.IsZero() {
		return 0
	}
	return p.EndTime.Sub(p.StartTime)
}

func (p PenaltyLap) AverageSpeed() float64 {
	durationSeconds := p.Duration().Seconds()
	if durationSeconds <= 0 || p.Distance <= 0 {
		return 0.0
	}
	return p.Distance / durationSeconds
}

type FiringRangeVisit struct {
	EnterTime time.Time
	ExitTime  time.Time
	Hits      int
	Shots     int
}

type CompetitorStatus string

const (
	StatusRegistered   CompetitorStatus = "Registered"
	StatusScheduled    CompetitorStatus = "Scheduled"
	StatusOnStartLine  CompetitorStatus = "OnStartLine"
	StatusStarted      CompetitorStatus = "Started"
	StatusOnLap        CompetitorStatus = "OnLap"
	StatusOnRange      CompetitorStatus = "OnRange"
	StatusInPenalty    CompetitorStatus = "InPenalty"
	StatusFinished     CompetitorStatus = "Finished"
	StatusNotFinished  CompetitorStatus = "NotFinished"
	StatusDisqualified CompetitorStatus = "Disqualified"
	StatusNotStarted   CompetitorStatus = "NotStarted"
)

type Competitor struct {
	ID                 int
	Status             CompetitorStatus
	ScheduledStartTime time.Time
	ActualStartTime    time.Time
	FinishTime         time.Time
	Comment            string

	LapsCompleted    []Lap
	CurrentLapNumber int
	CurrentLapStart  time.Time

	PenaltyLapsCompleted []PenaltyLap
	CurrentPenaltyStart  time.Time
	CurrentPenaltyDist   float64

	FiringRangeVisits []FiringRangeVisit
	CurrentRangeVisit *FiringRangeVisit
	CurrentRangeHits  int
	LastMisses        int

	TotalShots int
	TotalHits  int

	LastEventTime time.Time
}

type Event struct {
	Time         time.Time
	ID           int
	CompetitorID int
	ExtraParams  []string
	RawLine      string
}

func parseDuration(durationStr string) (time.Duration, error) {
	parts := strings.Split(durationStr, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid duration format: %s", durationStr)
	}

	secsParts := strings.Split(parts[2], ".")
	var d time.Duration
	var err error

	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	s, err := strconv.Atoi(secsParts[0])
	if err != nil {
		return 0, err
	}

	d = time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second

	if len(secsParts) == 2 {
		ms, err := strconv.Atoi(secsParts[1])
		if err != nil {
			return 0, err
		}
		msStr := secsParts[1]
		for len(msStr) < 3 {
			msStr += "0"
		}
		ms, err = strconv.Atoi(msStr)
		if err != nil {
			return 0, err
		}
		d += time.Duration(ms) * time.Millisecond
	}

	return d, nil
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}

	totalSeconds := int64(d.Seconds())
	milliseconds := d.Milliseconds() % 1000

	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, milliseconds)
}

func loadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("error opening config file: %w", err)
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	var config Config
	err = json.Unmarshal(bytes, &config)
	if err != nil {
		return nil, fmt.Errorf("error parsing config JSON: %w", err)
	}

	config.parsedStart, err = time.Parse(configTimeLayout, config.Start)
	if err != nil {
		return nil, fmt.Errorf("error parsing config start time: %w", err)
	}

	config.parsedStartDelta, err = parseDuration(config.StartDelta)
	if err != nil {
		return nil, fmt.Errorf("error parsing config start delta '%s': %w", config.StartDelta, err)
	}

	return &config, nil
}

func parseEvent(line string) (*Event, error) {
	line = strings.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}

	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid event format: %s", line)
	}

	timeStr := parts[0]
	eventIDStr := parts[1]
	competitorIDStr := ""
	extraParamsStr := ""

	remainingParts := strings.SplitN(parts[2], " ", 2)
	competitorIDStr = remainingParts[0]
	if len(remainingParts) > 1 {
		extraParamsStr = remainingParts[1]
	}

	eventTime, err := time.Parse(eventTimeLayout, timeStr)
	if err != nil {
		return nil, fmt.Errorf("invalid time format %s: %w", timeStr, err)
	}

	eventID, err := strconv.Atoi(eventIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid event ID %s: %w", eventIDStr, err)
	}

	competitorID, err := strconv.Atoi(competitorIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid competitor ID %s: %w", competitorIDStr, err)
	}

	var extraParams []string
	if extraParamsStr != "" {
		if eventID == 11 {
			extraParams = []string{extraParamsStr}
		} else {
			extraParams = strings.Fields(extraParamsStr)
		}
	}

	return &Event{
		Time:         eventTime,
		ID:           eventID,
		CompetitorID: competitorID,
		ExtraParams:  extraParams,
		RawLine:      line,
	}, nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Println("usage: go run main.go <config.json> <event>")
		os.Exit(1)
	}

	configFile := os.Args[1]
	eventsFile := os.Args[2]

	config, err := loadConfig(configFile)
	if err != nil {
		fmt.Printf("error loading configuration: %v\n", err)
		os.Exit(1)
	}

	eventsLogFile, err := os.Open(eventsFile)
	if err != nil {
		fmt.Printf("error opening events file: %v\n", err)
		os.Exit(1)
	}
	defer eventsLogFile.Close()

	competitors := make(map[int]*Competitor)
	var eventProcessingOrder []*Event
	var outputLog []string

	scanner := bufio.NewScanner(eventsLogFile)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		event, err := parseEvent(line)
		if err != nil {
			fmt.Printf("error parsing event on line %d: %v\n", lineNumber, err)
			continue
		}
		if event != nil {
			eventProcessingOrder = append(eventProcessingOrder, event)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("error reading events file: %v\n", err)
		os.Exit(1)
	}

	var lastProcessedTime time.Time

	for _, event := range eventProcessingOrder {
		for _, comp := range competitors {
			if comp.Status == StatusScheduled || comp.Status == StatusOnStartLine {
				if !comp.ScheduledStartTime.IsZero() && comp.ActualStartTime.IsZero() {
					allowedStartWindowEnd := comp.ScheduledStartTime.Add(config.parsedStartDelta)
					if event.Time.After(allowedStartWindowEnd) {
						if comp.Status != StatusDisqualified && comp.Status != StatusNotStarted {
							comp.Status = StatusNotStarted
							comp.FinishTime = event.Time
							msg := fmt.Sprintf("The competitor(%d) is disqualified (Did not start)", comp.ID)
							outputLog = append(outputLog, fmt.Sprintf("%s %s", event.Time.Format(eventTimeLayout), msg))
						}
					}
				}
			}
		}

		competitor, exists := competitors[event.CompetitorID]

		if event.ID == 1 {
			if !exists {
				competitor = &Competitor{
					ID:                   event.CompetitorID,
					Status:               StatusRegistered,
					LapsCompleted:        []Lap{},
					PenaltyLapsCompleted: []PenaltyLap{},
					FiringRangeVisits:    []FiringRangeVisit{},
					CurrentLapNumber:     0,
				}
				competitors[event.CompetitorID] = competitor
				msg := fmt.Sprintf("The competitor(%d) registered", event.CompetitorID)
				outputLog = append(outputLog, fmt.Sprintf("%s %s", event.Time.Format(eventTimeLayout), msg))
			}
		} else if !exists {
			fmt.Printf("Warning: Event %d for unknown competitor %d at %s\n", event.ID, event.CompetitorID, event.Time.Format(eventTimeLayout))
			continue
		} else if competitor.Status == StatusFinished || competitor.Status == StatusNotFinished || competitor.Status == StatusDisqualified || competitor.Status == StatusNotStarted {
			continue
		}

		competitor.LastEventTime = event.Time

		logMsg := ""

		switch event.ID {
		case 2:
			if len(event.ExtraParams) < 1 {
				fmt.Printf("event 2 missing start time for competitor %d at %s\n", event.CompetitorID, event.Time.Format(eventTimeLayout))
				continue
			}
			startTimeStr := event.ExtraParams[0]
			scheduledTime, err := time.Parse(timeLayout, startTimeStr)
			if err != nil {
				fmt.Printf("event 2 invalid start time format '%s' for competitor %d: %v\n", startTimeStr, event.CompetitorID, err)
				continue
			}
			baseDate := config.parsedStart.Truncate(24 * time.Hour)
			competitor.ScheduledStartTime = baseDate.Add(time.Duration(scheduledTime.Hour())*time.Hour + time.Duration(scheduledTime.Minute())*time.Minute + time.Duration(scheduledTime.Second())*time.Second + time.Duration(scheduledTime.Nanosecond()))

			competitor.Status = StatusScheduled
			logMsg = fmt.Sprintf("The start time for the competitor(%d) was set by a draw to %s", event.CompetitorID, startTimeStr)

		case 3:
			if competitor.Status == StatusScheduled {
				competitor.Status = StatusOnStartLine
				logMsg = fmt.Sprintf("The competitor(%d) is on the start line", event.CompetitorID)
			} else {
				continue
			}

		case 4:
			allowedStartWindowEnd := competitor.ScheduledStartTime.Add(config.parsedStartDelta)
			if event.Time.After(allowedStartWindowEnd) && !competitor.ScheduledStartTime.IsZero() {
				if competitor.Status != StatusNotStarted {
					competitor.Status = StatusNotStarted
					competitor.FinishTime = event.Time
					msg := fmt.Sprintf("The competitor(%d) is disqualified (Started too late)", event.CompetitorID)
					outputLog = append(outputLog, fmt.Sprintf("%s %s", event.Time.Format(eventTimeLayout), msg))
					outputLog = append(outputLog, fmt.Sprintf("%s The competitor(%d) is disqualified", event.Time.Format(eventTimeLayout), event.CompetitorID))
				}
				continue
			}

			if competitor.Status == StatusOnStartLine || competitor.Status == StatusScheduled {
				competitor.ActualStartTime = event.Time
				competitor.Status = StatusStarted
				competitor.CurrentLapNumber = 1
				competitor.CurrentLapStart = event.Time
				logMsg = fmt.Sprintf("The competitor(%d) has started", event.CompetitorID)
			} else {
				continue
			}

		case 5:
			if competitor.Status == StatusStarted || competitor.Status == StatusOnLap {
				competitor.Status = StatusOnRange
				rangeNumStr := "unknown"
				if len(event.ExtraParams) > 0 {
					rangeNumStr = event.ExtraParams[0]
				}
				competitor.CurrentRangeVisit = &FiringRangeVisit{EnterTime: event.Time, Shots: 5} // Assume 5 shots
				competitor.CurrentRangeHits = 0                                                   // Reset hits counter for this visit
				logMsg = fmt.Sprintf("The competitor(%d) is on the firing range(%s)", event.CompetitorID, rangeNumStr)
			} else {
				continue
			}

		case 6:
			if competitor.Status == StatusOnRange && competitor.CurrentRangeVisit != nil {
				competitor.CurrentRangeHits++
				targetNumStr := "unknown"
				if len(event.ExtraParams) > 0 {
					targetNumStr = event.ExtraParams[0]
				}
				logMsg = fmt.Sprintf("The target(%s) has been hit by competitor(%d)", targetNumStr, event.CompetitorID)
			} else {
				continue
			}

		case 7:
			if competitor.Status == StatusOnRange && competitor.CurrentRangeVisit != nil {
				competitor.Status = StatusOnLap
				competitor.CurrentRangeVisit.ExitTime = event.Time
				competitor.CurrentRangeVisit.Hits = competitor.CurrentRangeHits
				competitor.TotalHits += competitor.CurrentRangeVisit.Hits
				competitor.TotalShots += competitor.CurrentRangeVisit.Shots
				competitor.LastMisses = competitor.CurrentRangeVisit.Shots - competitor.CurrentRangeVisit.Hits
				competitor.FiringRangeVisits = append(competitor.FiringRangeVisits, *competitor.CurrentRangeVisit)
				competitor.CurrentRangeVisit = nil

				if competitor.Status != StatusFinished && competitor.Status != StatusNotFinished && competitor.Status != StatusDisqualified {
					logMsg = fmt.Sprintf("The competitor(%d) left the firing range", event.CompetitorID)
					if competitor.LastMisses == 0 {
						competitor.Status = StatusOnLap
					}
				} else {
					continue
				}

			} else {
				continue
			}

		case 8:
			if (competitor.Status == StatusOnLap || competitor.Status == StatusStarted) && competitor.LastMisses > 0 { // Should happen after leaving range with misses
				competitor.Status = StatusInPenalty
				competitor.CurrentPenaltyStart = event.Time
				competitor.CurrentPenaltyDist = float64(competitor.LastMisses) * config.PenaltyLen
				logMsg = fmt.Sprintf("The competitor(%d) entered the penalty laps", event.CompetitorID)
			} else {
				continue
			}

		case 9:
			if competitor.Status == StatusInPenalty {
				competitor.Status = StatusOnLap
				penalty := PenaltyLap{
					StartTime: competitor.CurrentPenaltyStart,
					EndTime:   event.Time,
					Distance:  competitor.CurrentPenaltyDist,
				}
				competitor.PenaltyLapsCompleted = append(competitor.PenaltyLapsCompleted, penalty)
				competitor.CurrentPenaltyStart = time.Time{}
				competitor.CurrentPenaltyDist = 0
				competitor.LastMisses = 0
				logMsg = fmt.Sprintf("The competitor(%d) left the penalty laps", event.CompetitorID)
			} else {
				continue
			}

		case 10:
			if competitor.Status == StatusOnLap || competitor.Status == StatusStarted {
				if competitor.LastMisses > 0 {
					continue
				}

				lap := Lap{
					Number:    competitor.CurrentLapNumber,
					StartTime: competitor.CurrentLapStart,
					EndTime:   event.Time,
					Distance:  config.LapLen,
				}
				competitor.LapsCompleted = append(competitor.LapsCompleted, lap)
				//logMsg = fmt.Sprintf("The competitor(%d) ended the main lap", event.CompetitorID)
				outputLog = append(outputLog, fmt.Sprintf("%s The competitor(%d) ended the main lap", event.Time.Format(eventTimeLayout), event.CompetitorID))

				if competitor.CurrentLapNumber == config.Laps {
					competitor.Status = StatusFinished
					competitor.FinishTime = event.Time
					finishMsg := fmt.Sprintf("%s The competitor(%d) has finished", event.Time.Format(eventTimeLayout), event.CompetitorID)
					outputLog = append(outputLog, finishMsg)
				} else {
					competitor.CurrentLapNumber++
					competitor.CurrentLapStart = event.Time
					competitor.Status = StatusOnLap
				}
			} else {
				continue
			}

		case 11:
			if competitor.Status != StatusFinished && competitor.Status != StatusNotFinished && competitor.Status != StatusDisqualified {
				competitor.Status = StatusNotFinished
				competitor.FinishTime = event.Time
				if len(event.ExtraParams) > 0 {
					competitor.Comment = event.ExtraParams[0]
					logMsg = fmt.Sprintf("The competitor(%d) can`t continue: %s", event.CompetitorID, competitor.Comment)
				} else {
					logMsg = fmt.Sprintf("The competitor(%d) can`t continue", event.CompetitorID)
				}
			} else {
				continue
			}
		}

		if logMsg != "" {
			outputLog = append(outputLog, fmt.Sprintf("%s %s", event.Time.Format(eventTimeLayout), logMsg))
		}

		lastProcessedTime = event.Time
	}

	for _, comp := range competitors {
		if comp.Status == StatusScheduled || comp.Status == StatusOnStartLine {
			if !comp.ScheduledStartTime.IsZero() && comp.ActualStartTime.IsZero() {
				if comp.Status != StatusNotStarted {
					comp.Status = StatusNotStarted
					comp.FinishTime = lastProcessedTime
					msg := fmt.Sprintf("The competitor(%d) is disqualified (Did not start by end of log)", comp.ID)
					outputLog = append(outputLog, fmt.Sprintf("%s %s", lastProcessedTime.Format(eventTimeLayout), msg))
				}
			}
		} else if comp.Status == StatusStarted || comp.Status == StatusOnLap || comp.Status == StatusOnRange || comp.Status == StatusInPenalty {
			if comp.Status != StatusNotFinished {
				comp.Status = StatusNotFinished
				comp.FinishTime = lastProcessedTime
				comp.Comment = "Did not finish before end of log"
				msg := fmt.Sprintf("The competitor(%d) marked as NotFinished at end of log", comp.ID)
				outputLog = append(outputLog, fmt.Sprintf("%s %s", lastProcessedTime.Format(eventTimeLayout), msg))
			}
		}
	}

	// Вывод

	// Вывод выходного лога в консоль
	fmt.Println("Output Log")
	for _, logEntry := range outputLog {
		fmt.Println(logEntry)
	}
	fmt.Println("End Output Log")
	fmt.Println() // Spacer

	// Сохраниение выходного лога в файл

	outputFile1, err := os.Create("output_log.txt")
	if err != nil {
		fmt.Printf("error creating output log file: %v\n", err)
		os.Exit(1)
	}
	defer outputFile1.Close()
	writer1 := bufio.NewWriter(outputFile1)
	for _, logEntry := range outputLog {
		writer1.WriteString(logEntry + "\n")
	}
	writer1.Flush()

	// Подготовка и вывод финального отчета
	competitorList := make([]*Competitor, 0, len(competitors))
	for _, c := range competitors {
		competitorList = append(competitorList, c)
	}

	sort.Slice(competitorList, func(i, j int) bool {
		ci := competitorList[i]
		cj := competitorList[j]

		statusOrder := map[CompetitorStatus]int{
			StatusFinished:     0,
			StatusNotFinished:  1,
			StatusNotStarted:   2,
			StatusDisqualified: 3,
			StatusOnLap:        4,
			StatusInPenalty:    4,
			StatusOnRange:      4,
			StatusStarted:      4,
			StatusOnStartLine:  4,
			StatusScheduled:    4,
			StatusRegistered:   4,
		}

		statusI := statusOrder[ci.Status]
		statusJ := statusOrder[cj.Status]

		if statusI != statusJ {
			return statusI < statusJ
		}

		if ci.Status == StatusFinished && cj.Status == StatusFinished {
			totalTimeI := ci.FinishTime.Sub(ci.ScheduledStartTime)
			totalTimeJ := cj.FinishTime.Sub(cj.ScheduledStartTime)
			return totalTimeI < totalTimeJ
		}

		return ci.ID < cj.ID

	})

	outputFile2, err := os.Create("result_table.txt")
	if err != nil {
		fmt.Printf("error creating result table file: %v\n", err)
		os.Exit(1)
	}
	defer outputFile2.Close()
	writer2 := bufio.NewWriter(outputFile2)

	fmt.Println("Resulting Table")
	for _, c := range competitorList {
		statusStr := ""
		totalTimeStr := ""

		switch c.Status {
		case StatusFinished:
			statusStr = "[Finished]"
			if !c.FinishTime.IsZero() && !c.ScheduledStartTime.IsZero() {
				totalTime := c.FinishTime.Sub(c.ScheduledStartTime)
				totalTimeStr = formatDuration(totalTime)
			} else {
				totalTimeStr = "ERR: Missing Times"
			}
		case StatusNotFinished:
			statusStr = "[NotFinished]"
			totalTimeStr = "NotFinished"
			if c.Comment != "" {
				totalTimeStr += " (" + c.Comment + ")"
			}
		case StatusNotStarted:
			statusStr = "[NotStarted]"
			totalTimeStr = "NotStarted"
		case StatusDisqualified:
			statusStr = "[Disqualified]"
			totalTimeStr = "Disqualified"
		default:
			statusStr = fmt.Sprintf("[%s]", c.Status)
			totalTimeStr = string(c.Status)
		}

		var lapDetails []string
		for i := 0; i < config.Laps; i++ {
			detail := "{,}"
			if i < len(c.LapsCompleted) {
				lap := c.LapsCompleted[i]
				if lap.Duration() > 0 {
					detail = fmt.Sprintf("{%s, %.3f}", formatDuration(lap.Duration()), lap.AverageSpeed())
				} else {
					detail = fmt.Sprintf("{%s, 0.000}", formatDuration(lap.Duration()))
				}

			}
			lapDetails = append(lapDetails, detail)
		}
		lapsStr := strings.Join(lapDetails, " ")

		var totalPenaltyDuration time.Duration
		var totalPenaltyDistance float64
		for _, p := range c.PenaltyLapsCompleted {
			totalPenaltyDuration += p.Duration()
			totalPenaltyDistance += p.Distance
		}
		penaltyAvgSpeed := 0.0
		if totalPenaltyDuration.Seconds() > 0 && totalPenaltyDistance > 0 {
			penaltyAvgSpeed = totalPenaltyDistance / totalPenaltyDuration.Seconds()
		}
		penaltyStr := fmt.Sprintf("{%s, %.3f}", formatDuration(totalPenaltyDuration), penaltyAvgSpeed)

		shootingStr := fmt.Sprintf("%d/%d", c.TotalHits, c.TotalShots)

		// Вывод и сохранение в файл финального отчета
		fmt.Printf("%s %d %s %s %s %s\n",
			statusStr,
			c.ID,
			totalTimeStr,
			lapsStr,
			penaltyStr,
			shootingStr,
		)
		writer2.WriteString(fmt.Sprintf("%s %d %s %s %s %s\n",
			statusStr,
			c.ID,
			totalTimeStr,
			lapsStr,
			penaltyStr,
			shootingStr,
		))
	}
	fmt.Println("End Resulting Table")
	writer2.Flush()
}
