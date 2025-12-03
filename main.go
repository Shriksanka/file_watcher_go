package main

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"image/color"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/fsnotify/fsnotify"
)

const (
	apiEndpoint = "https://otlglcc10g.execute-api.ap-south-1.amazonaws.com/api/statements/upload"
	fileStabilityDelay = 2 * time.Second
	checkInterval = 500 * time.Millisecond
)

var (
	processedFiles = make(map[string]time.Time)
	checkingFiles = make(map[string]*fileCheck)
	mu sync.RWMutex
	fileNamePattern = regexp.MustCompile(`^.{3,5}__`)
)

type fileCheck struct {
	lastSize    int64
	lastModTime time.Time
	stableSince time.Time
	timer       *time.Timer
}

func matchesFileNamePattern(fileName string) bool {
	nameWithoutExt := fileName
	if idx := strings.LastIndex(fileName, "."); idx > 0 {
		nameWithoutExt = fileName[:idx]
	}
	
	if len(nameWithoutExt) < 5 {
		return false
	}
	
	// Проверяем, что начало файла (от 3 до 5 символов) состоит только из букв,
	// затем идут два нижних подчеркивания __
	for prefixLen := 3; prefixLen <= 5 && prefixLen+2 <= len(nameWithoutExt); prefixLen++ {
		// Проверяем, что первые prefixLen символов - это только буквы
		prefix := nameWithoutExt[:prefixLen]
		isAllLetters := true
		for _, r := range prefix {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				isAllLetters = false
				break
			}
		}
		
		// Если префикс состоит только из букв и после него идут __
		if isAllLetters && nameWithoutExt[prefixLen:prefixLen+2] == "__" {
			return true
		}
	}
	
	return false
}

func getDownloadsPath() (string, error) {
	var downloadsPath string

	switch runtime.GOOS {
	case "windows":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("не удалось получить домашнюю директорию: %v", err)
		}
		downloadsPath = filepath.Join(homeDir, "Downloads")
		
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("не удалось получить домашнюю директорию: %v", err)
		}
		downloadsPath = filepath.Join(homeDir, "Downloads")
		
	case "linux":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("не удалось получить домашнюю директорию: %v", err)
		}
		downloadsPath = filepath.Join(homeDir, "Downloads")
		
	default:
		return "", fmt.Errorf("неподдерживаемая ОС: %s", runtime.GOOS)
	}

	if _, err := os.Stat(downloadsPath); os.IsNotExist(err) {
		return "", fmt.Errorf("папка Downloads не найдена: %s", downloadsPath)
	}

	return downloadsPath, nil
}

func isFileStable(filePath string) bool {
	info, err := os.Stat(filePath)
	if err != nil {
		return false
	}

	mu.Lock()
	defer mu.Unlock()

	check, exists := checkingFiles[filePath]
	if !exists {
		checkingFiles[filePath] = &fileCheck{
			lastSize:    info.Size(),
			lastModTime: info.ModTime(),
			stableSince: time.Now(),
		}
		return false
	}

	if info.Size() != check.lastSize || !info.ModTime().Equal(check.lastModTime) {
		check.lastSize = info.Size()
		check.lastModTime = info.ModTime()
		check.stableSince = time.Now()
		return false
	}

	if time.Since(check.stableSince) >= fileStabilityDelay {
		return true
	}

	return false
}

func uploadFile(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("не удалось открыть файл: %v", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("не удалось создать form file: %v", err)
	}

	_, err = io.Copy(part, file)
	if err != nil {
		return fmt.Errorf("не удалось скопировать файл: %v", err)
	}

	err = writer.Close()
	if err != nil {
		return fmt.Errorf("не удалось закрыть writer: %v", err)
	}

	req, err := http.NewRequest("POST", apiEndpoint, body)
	if err != nil {
		return fmt.Errorf("не удалось создать запрос: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка при отправке запроса: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("сервер вернул ошибку: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

func handleFile(filePath string, logChan chan<- string, isActive func() bool) {
	if !isActive() {
		return
	}

	mu.Lock()
	if _, exists := processedFiles[filePath]; exists {
		mu.Unlock()
		return
	}
	mu.Unlock()

	info, err := os.Stat(filePath)
	if err != nil {
		return
	}
	if info.IsDir() {
		return
	}

	fileName := filepath.Base(filePath)
	if !matchesFileNamePattern(fileName) {
		return
	}

	if !isFileStable(filePath) {
		if !isActive() {
			return
		}
		time.AfterFunc(checkInterval, func() {
			handleFile(filePath, logChan, isActive)
		})
		return
	}

	if !isActive() {
		return
	}
	
	mu.Lock()
	if _, exists := processedFiles[filePath]; exists {
		mu.Unlock()
		return
	}
	processedFiles[filePath] = time.Now()
	delete(checkingFiles, filePath)
	mu.Unlock()

	logChan <- fmt.Sprintf("Обработка файла: %s", fileName)

	err = uploadFile(filePath)
	if err != nil {
		logChan <- fmt.Sprintf("Ошибка при отправке %s: %v", fileName, err)
		mu.Lock()
		delete(processedFiles, filePath)
		mu.Unlock()
		return
	}

	logChan <- fmt.Sprintf("Файл успешно отправлен: %s", fileName)
}

func watchDownloads(stopChan <-chan bool, logChan chan<- string, isActive func() bool) error {
	downloadsPath, err := getDownloadsPath()
	if err != nil {
		return err
	}

	logChan <- fmt.Sprintf("Отслеживание папки: %s", downloadsPath)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("не удалось создать watcher: %v", err)
	}
	defer watcher.Close()

	err = watcher.Add(downloadsPath)
	if err != nil {
		return fmt.Errorf("не удалось добавить папку в watcher: %v", err)
	}

	for {
		select {
		case _, ok := <-stopChan:
			if !ok {
				logChan <- "Обработка файлов остановлена"
				return nil
			}
			logChan <- "Обработка файлов остановлена"
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("канал событий закрыт")
			}

			if !isActive() {
				continue
			}

			if event.Op&fsnotify.Create == fsnotify.Create {
				time.Sleep(100 * time.Millisecond)
				go handleFile(event.Name, logChan, isActive)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("канал ошибок закрыт")
			}
			logChan <- fmt.Sprintf("Ошибка watcher: %v", err)
		}
	}
}

func main() {
	myApp := app.New()
	myApp.Settings().SetTheme(&customTheme{})
	
	myWindow := myApp.NewWindow("File Upload Monitor")
	myWindow.Resize(fyne.NewSize(600, 500))
	myWindow.CenterOnScreen()

	var isProcessingActive bool
	var processingMu sync.Mutex
	var stopChan chan bool
	
	isActive := func() bool {
		processingMu.Lock()
		defer processingMu.Unlock()
		return isProcessingActive
	}

	logChan := make(chan string, 100)

	logText := widget.NewMultiLineEntry()
	logText.Disable()
	logText.Wrapping = fyne.TextWrapWord
	logText.Validator = nil
	logAreaBg := canvas.NewRectangle(color.RGBA{R: 0x27, G: 0x32, B: 0x41, A: 255})
	logContainer := container.NewScroll(logText)
	logContainer.SetMinSize(fyne.NewSize(580, 400))
	logAreaContainer := container.NewMax(logAreaBg, logContainer)

	startStopBtn := widget.NewButton("START", nil)
	startStopBtn.Importance = widget.HighImportance

	startStopBtn.OnTapped = func() {
		processingMu.Lock()
		wasActive := isProcessingActive
		processingMu.Unlock()

		if wasActive {
			processingMu.Lock()
			isProcessingActive = false
			processingMu.Unlock()
			
			if stopChan != nil {
				close(stopChan)
				stopChan = nil
			}
			
			startStopBtn.SetText("START")
			startStopBtn.Importance = widget.HighImportance
			startStopBtn.Refresh()
		} else {
			processingMu.Lock()
			isProcessingActive = true
			processingMu.Unlock()
			
			stopChan = make(chan bool)
			
			startStopBtn.SetText("STOP")
			startStopBtn.Importance = widget.DangerImportance
			startStopBtn.Refresh()
			
			go func() {
				err := watchDownloads(stopChan, logChan, isActive)
				if err != nil && err.Error() != "канал событий закрыт" {
					logChan <- fmt.Sprintf("Ошибка: %v", err)
				}
				processingMu.Lock()
				if isProcessingActive {
					isProcessingActive = false
					logChan <- "Обработка остановлена"
					startStopBtn.SetText("START")
					startStopBtn.Importance = widget.HighImportance
					startStopBtn.Refresh()
				}
				processingMu.Unlock()
			}()
		}
	}

	go func() {
		for logMsg := range logChan {
			timestamp := time.Now().Format("15:04:05")
			logLine := fmt.Sprintf("[%s] %s", timestamp, logMsg)
			
			currentText := logText.Text
			var newText string
			if currentText == "" {
				newText = logLine
			} else {
				newText = currentText + "\n" + logLine
			}
			lines := strings.Split(newText, "\n")
			if len(lines) > 100 {
				lines = lines[len(lines)-100:]
				newText = strings.Join(lines, "\n")
			}
			logText.SetText(newText)
			logContainer.ScrollToBottom()
		}
	}()

	buttonContainer := container.NewCenter(startStopBtn)

	mainBg := canvas.NewRectangle(color.RGBA{R: 0x1E, G: 0x25, B: 0x2E, A: 255})
	
	content := container.NewBorder(
		nil,
		buttonContainer,
		nil,
		nil,
		logAreaContainer,
	)
	
	mainContainer := container.NewMax(mainBg, content)

	myWindow.SetContent(mainContainer)
	myWindow.ShowAndRun()
}

type customTheme struct{}

func (t *customTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return color.RGBA{R: 0x1E, G: 0x25, B: 0x2E, A: 255}
	case theme.ColorNameButton:
		return color.RGBA{R: 0x5B, G: 0xC8, B: 0xAC, A: 255}
	case theme.ColorNameError:
		return color.RGBA{R: 0xEF, G: 0x47, B: 0x6F, A: 255}
	case theme.ColorNameForeground:
		return color.RGBA{R: 0xE0, G: 0xE3, B: 0xE7, A: 255}
	case theme.ColorNameInputBackground:
		return color.RGBA{R: 0x27, G: 0x32, B: 0x41, A: 255}
	case theme.ColorNameInputBorder:
		return color.RGBA{R: 0x2F, G: 0x3D, B: 0x4E, A: 255}
	case theme.ColorNamePlaceHolder:
		return color.RGBA{R: 0xE0, G: 0xE3, B: 0xE7, A: 200}
	default:
		return theme.LightTheme().Color(name, variant)
	}
}

func (t *customTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.LightTheme().Font(style)
}

func (t *customTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.LightTheme().Icon(name)
}

func (t *customTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.LightTheme().Size(name)
}
