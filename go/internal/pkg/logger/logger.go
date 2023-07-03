package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

type UTCFormatter struct {
	logrus.Formatter
}

func (u UTCFormatter) Format(e *logrus.Entry) ([]byte, error) {
	e.Time = e.Time.UTC()
	return u.Formatter.Format(e)
}

func NewLogger() *logrus.Logger {
	log := logrus.New()

	logsDir := "logs"
	fileName := filepath.Join(logsDir, fmt.Sprintf("logfile-%s.log", time.Now().UTC().Format("020120061504")))
	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal(err)
	}
	log.SetOutput(file)
	log.SetFormatter(UTCFormatter{&logrus.TextFormatter{}})

	return log
}
