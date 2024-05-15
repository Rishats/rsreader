package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/joho/godotenv"
)

const (
	defaultURL                  = "https://api.raspberryshake.org/query/objects.json"
	defaultLogFile              = "sensor.log"
	defaultSoundHighThreshold   = 3500.0
	defaultSoundMediumThreshold = 350.0
	maxLogSize                  = 100 * 1024 * 1024 // 100 MB
)

var (
	lastTimestamp        int64
	mutex                sync.Mutex
	url                  string
	logFile              string
	sensorID             string
	soundHighThreshold   float64
	soundMediumThreshold float64
)

// Data structure for JSON response
type Response struct {
	Request struct {
		GM struct {
			List []struct {
				ID        string  `json:"id"`
				Acc       float64 `json:"acc"`
				Vel       float64 `json:"vel"`
				Disp      float64 `json:"disp"`
				Timestamp int64   `json:"timestamp"`
			} `json:"list"`
		} `json:"GM"`
	} `json:"request"`
}

func loadEnv() {
	// Check if .env file exists and load it if it does
	if _, err := os.Stat(".env"); err == nil {
		_ = godotenv.Load(".env")
	} else if _, err := os.Stat(".env.example"); err == nil {
		// If .env does not exist, check if .env.example exists and load it if it does
		_ = godotenv.Load(".env.example")
	}
}

func init() {
	// Load environment variables in order of priority: system > .env -> default
	loadEnv()

	// Load configuration from environment variables
	url = getEnv("URL", defaultURL)
	logFile = getEnv("LOG_FILE", defaultLogFile)
	sensorID = getEnv("SENSOR_ID", "AM.R1B7B")

	soundHighThreshold = getEnvAsFloat("SOUND_HIGH_THRESHOLD", defaultSoundHighThreshold)
	soundMediumThreshold = getEnvAsFloat("SOUND_MEDIUM_THRESHOLD", defaultSoundMediumThreshold)
}

func main() {
	// Display introduction and legend
	displayIntroAndLegend()

	// Wait for user to press space
	fmt.Println("Press space and Enter to start.")
	var input string
	fmt.Scanln(&input)

	// Clear screen
	clearScreen()

	// Start data fetching loop
	for {
		var wg sync.WaitGroup
		results := make(chan *Response, 5)
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				data := fetchData()
				if data != nil {
					results <- data
				}
			}()
		}
		go func() {
			wg.Wait()
			close(results)
		}()

		for data := range results {
			targetData := findData(data)
			if targetData != nil && targetData.Timestamp > lastTimestamp {
				formattedOutput := formatData(targetData)
				lastTimestamp = targetData.Timestamp
				clearScreen()
				fmt.Print(formattedOutput)
				playSound(targetData)
				break
			}
		}

		time.Sleep(1 * time.Second)
	}
}

func displayIntroAndLegend() {
	intro := `
Welcome to the Ground Motion Monitoring System!
========================================
This script will fetch data from the sensor and display
ground motion information in real time.
Press space to start.
========================================
`
	fmt.Println(intro)

	legend := `
Ground Motion Legend
========================================
Parameter       Description                              Units                Desired Range
----------------------------------------
Acceleration    Peak ground acceleration in last 10s    micrometers/sec²        Noise level < 0.5
Velocity        Peak ground velocity in last 10s        micrometers/sec         Noise level < 0.1
Displacement    Peak ground displacement in last 10s    micrometers             Noise level ~0
========================================
`

	fmt.Println(legend)

	// Velocity color legend with highlighting
	fmt.Println("\nVelocity Color Legend")
	fmt.Println("----------------------------------------")
	fmt.Printf("%s\n", color.BlueString("0.0 - 0.2 µm/s"))
	fmt.Printf("%s\n", color.CyanString("0.2 - 0.4 µm/s"))
	fmt.Printf("%s\n", color.HiGreenString("0.4 - 0.8 µm/s"))
	fmt.Printf("%s\n", color.HiYellowString("0.8 - 1.5 µm/s"))
	fmt.Printf("%s\n", color.YellowString("1.5 - 4.0 µm/s"))
	fmt.Printf("%s\n", color.HiMagentaString("4.0 - 12.0 µm/s"))
	fmt.Printf("%s\n", color.MagentaString("12.0 - 30.0 µm/s"))
	fmt.Printf("%s\n", color.HiRedString("30.0 - 60.0 µm/s"))
	fmt.Printf("%s\n", color.RedString("60.0 - 1000.0 µm/s"))
	fmt.Println("----------------------------------------")

	// Acceleration color legend with highlighting
	fmt.Println("\nAcceleration Color Legend")
	fmt.Println("----------------------------------------")
	fmt.Printf("%s\n", color.GreenString("0 - 350 µm/s²"))
	fmt.Printf("%s\n", color.YellowString("350 - 3500 µm/s²"))
	fmt.Printf("%s\n", color.RedString("3500 - 10000 µm/s²"))
	fmt.Println("----------------------------------------")
}

func clearScreen() {
	fmt.Print("\033[H\033[J")
}

func checkLogSize() {
	fileInfo, err := os.Stat(logFile)
	if err == nil && fileInfo.Size() > maxLogSize {
		os.Remove(logFile)
	}
}

func logError(message string) {
	mutex.Lock()
	defer mutex.Unlock()
	checkLogSize()
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println(err)
		return
	}
	defer f.Close()
	logger := log.New(f, "", log.LstdFlags)
	logger.Println(message)
}

func fetchData() *Response {
	resp, err := http.Get(url)
	if err != nil {
		logError(fmt.Sprintf("Error fetching data: %v", err))
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logError(fmt.Sprintf("Error fetching data: %d", resp.StatusCode))
		return nil
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logError(fmt.Sprintf("Error reading response: %v", err))
		return nil
	}

	var data Response
	err = json.Unmarshal(body, &data)
	if err != nil {
		logError(fmt.Sprintf("Error parsing JSON: %v", err))
		return nil
	}

	return &data
}

func findData(data *Response) *struct {
	ID        string  `json:"id"`
	Acc       float64 `json:"acc"`
	Vel       float64 `json:"vel"`
	Disp      float64 `json:"disp"`
	Timestamp int64   `json:"timestamp"`
} {
	for _, item := range data.Request.GM.List {
		if item.ID == sensorID {
			return &item
		}
	}
	return nil
}

func formatData(data *struct {
	ID        string  `json:"id"`
	Acc       float64 `json:"acc"`
	Vel       float64 `json:"vel"`
	Disp      float64 `json:"disp"`
	Timestamp int64   `json:"timestamp"`
}) string {
	acc := data.Acc
	vel := data.Vel
	disp := data.Disp
	timestamp := data.Timestamp
	dt := time.Unix(timestamp/1000, 0).UTC()
	formattedTime := dt.Format(time.RFC3339)

	accColor := getColor(acc, []thresholdColor{
		{350, color.New(color.FgGreen)},
		{3500, color.New(color.FgYellow)},
		{10000, color.New(color.FgRed)},
	})
	velColor := getColor(vel, []thresholdColor{
		{0.2, color.New(color.FgBlue)},
		{0.4, color.New(color.FgCyan)},
		{0.8, color.New(color.FgHiGreen)},
		{1.5, color.New(color.FgHiYellow)},
		{4.0, color.New(color.FgYellow)},
		{12.0, color.New(color.FgHiMagenta)},
		{30.0, color.New(color.FgMagenta)},
		{60.0, color.New(color.FgHiRed)},
		{1000.0, color.New(color.FgRed)},
	})

	output := fmt.Sprintf(
		"\r\nGround Motion\n"+
			"%s\n"+
			"Data Time: %s\n"+
			"Acceleration: %s\n"+
			"Velocity: %s\n"+
			"Displacement: %.2f µm\n"+
			"%s\n",
		strings.Repeat("=", 40),
		formattedTime,
		accColor.Sprintf("%.2f µm/s²", acc),
		velColor.Sprintf("%.2f µm/s", vel),
		disp,
		strings.Repeat("=", 40),
	)
	return output
}

type thresholdColor struct {
	threshold float64
	color     *color.Color
}

func getColor(value float64, thresholds []thresholdColor) *color.Color {
	for _, tc := range thresholds {
		if value < tc.threshold {
			return tc.color
		}
	}
	return color.New(color.FgRed)
}

func playSound(data *struct {
	ID        string  `json:"id"`
	Acc       float64 `json:"acc"`
	Vel       float64 `json:"vel"`
	Disp      float64 `json:"disp"`
	Timestamp int64   `json:"timestamp"`
}) {
	var soundCmd *exec.Cmd

	switch {
	case data.Acc > soundHighThreshold:
		soundCmd = getSystemSoundCommand("high")
	case data.Acc > soundMediumThreshold:
		soundCmd = getSystemSoundCommand("medium")
	default:
		soundCmd = getSystemSoundCommand("low")
	}

	if soundCmd != nil {
		err := soundCmd.Run()
		if err != nil {
			logError(fmt.Sprintf("Error playing sound: %v", err))
		}
	}
}

func getSystemSoundCommand(level string) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		switch level {
		case "high":
			return exec.Command("cmd", "/c", "echo ^G") // Bell sound
		case "medium":
			return exec.Command("cmd", "/c", "echo ^G") // Bell sound
		case "low":
			//return exec.Command("cmd", "/c", "echo ^G") // Bell sound
		}
	case "darwin":
		switch level {
		case "high":
			return exec.Command("afplay", "/System/Library/Sounds/Hero.aiff")
		case "medium":
			return exec.Command("afplay", "/System/Library/Sounds/Submarine.aiff")
		case "low":
			//return exec.Command("afplay", "/System/Library/Sounds/Ping.aiff")
		}
	case "linux":
		switch level {
		case "high":
			return exec.Command("paplay", "/usr/share/sounds/freedesktop/stereo/complete.oga")
		case "medium":
			return exec.Command("paplay", "/usr/share/sounds/freedesktop/stereo/message.oga")
		case "low":
			//return exec.Command("paplay", "/usr/share/sounds/freedesktop/stereo/button.oga")
		}
	}
	return nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getEnvAsFloat(key string, fallback float64) float64 {
	if value, exists := os.LookupEnv(key); exists {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return fallback
}
