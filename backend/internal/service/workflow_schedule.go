package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

var workflowCronParser = cron.NewParser(
	cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

func nextWorkflowScheduledAt(raw json.RawMessage, after time.Time) (time.Time, error) {
	var config struct {
		EverySeconds   int    `json:"everySeconds"`
		CronExpression string `json:"cronExpression"`
		TimeZone       string `json:"timeZone"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return time.Time{}, errors.New("schedule config is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return time.Time{}, errors.New("schedule config is invalid")
	}
	config.CronExpression = strings.TrimSpace(config.CronExpression)
	config.TimeZone = strings.TrimSpace(config.TimeZone)
	hasInterval := config.EverySeconds != 0
	hasCron := config.CronExpression != "" || config.TimeZone != ""
	if hasInterval == hasCron {
		return time.Time{}, errors.New("schedule config must use either interval or cron")
	}
	if hasInterval {
		if config.EverySeconds < 60 || config.EverySeconds > 86400 {
			return time.Time{}, errors.New("schedule interval is invalid")
		}
		return after.UTC().Add(time.Duration(config.EverySeconds) * time.Second), nil
	}
	if config.CronExpression == "" || config.TimeZone == "" {
		return time.Time{}, errors.New("schedule cron expression and time zone are required")
	}
	if _, err := time.LoadLocation(config.TimeZone); err != nil {
		return time.Time{}, errors.New("schedule time zone is invalid")
	}
	schedule, err := workflowCronParser.Parse("CRON_TZ=" + config.TimeZone + " " + config.CronExpression)
	if err != nil {
		return time.Time{}, errors.New("schedule cron expression is invalid")
	}
	next := schedule.Next(after)
	if next.IsZero() {
		return time.Time{}, errors.New("schedule cron expression has no future occurrence")
	}
	return next.UTC(), nil
}
