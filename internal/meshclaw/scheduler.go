package meshclaw

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseSchedule parses schedule string like "every 1h", "every 15m", "every 1d"
func ParseSchedule(schedule string) (time.Duration, error) {
	schedule = strings.TrimSpace(strings.ToLower(schedule))

	// Remove "every " prefix
	schedule = strings.TrimPrefix(schedule, "every ")

	// Parse duration
	re := regexp.MustCompile(`^(\d+)\s*(s|m|h|d|sec|min|hour|day|seconds?|minutes?|hours?|days?)$`)
	matches := re.FindStringSubmatch(schedule)
	if len(matches) < 3 {
		return 0, fmt.Errorf("invalid schedule format: %s", schedule)
	}

	value, _ := strconv.Atoi(matches[1])
	unit := matches[2]

	switch {
	case strings.HasPrefix(unit, "s"):
		return time.Duration(value) * time.Second, nil
	case strings.HasPrefix(unit, "m"):
		return time.Duration(value) * time.Minute, nil
	case strings.HasPrefix(unit, "h"):
		return time.Duration(value) * time.Hour, nil
	case strings.HasPrefix(unit, "d"):
		return time.Duration(value) * 24 * time.Hour, nil
	}

	return 0, fmt.Errorf("invalid unit: %s", unit)
}

// RunScheduler runs scheduled tasks
func RunScheduler(cfg *Config, stopCh <-chan struct{}) {
	if cfg.Schedule == "" {
		return
	}

	interval, err := ParseSchedule(cfg.Schedule)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[scheduler] Invalid schedule: %v\n", err)
		return
	}

	fmt.Printf("[%s] Scheduler started: %s\n", cfg.Name, cfg.Schedule)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run immediately on start
	go runScheduledTask(cfg)

	for {
		select {
		case <-ticker.C:
			go runScheduledTask(cfg)
		case <-stopCh:
			fmt.Printf("[%s] Scheduler stopped\n", cfg.Name)
			return
		}
	}
}

func runScheduledTask(cfg *Config) {
	var result string

	// Run script if defined
	if cfg.ScheduleScript != "" {
		result = runScript(cfg.ScheduleScript)
	}

	// Run LLM task if defined
	if cfg.ScheduleTask != "" {
		prompt := cfg.ScheduleTask
		if result != "" {
			prompt = fmt.Sprintf("%s\n\nContext:\n%s", cfg.ScheduleTask, result)
		}
		result = processMessage(cfg, prompt)
	}

	// Send notification if configured
	if result != "" && cfg.Notify != nil {
		SendNotification(cfg, fmt.Sprintf("[%s] %s", cfg.Name, result))
	}

	fmt.Printf("[%s] Scheduled task completed\n", cfg.Name)
}

func runScript(script string) string {
	// Create temp file for script
	tmpfile, err := os.CreateTemp("", "meshclaw-*.sh")
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	defer os.Remove(tmpfile.Name())

	tmpfile.WriteString(script)
	tmpfile.Close()

	cmd := exec.Command("bash", tmpfile.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("ERROR: %v\n%s", err, string(output))
	}

	return strings.TrimSpace(string(output))
}
