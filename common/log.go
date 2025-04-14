package common

import (
	"fmt"
	"github.com/fatih/color"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
	"io"
	"os"
	"strings"
)

// writerHook 实现级别过滤的Hook
type writerHook struct {
	Writer    io.Writer
	LogLevels []logrus.Level
	Formatter logrus.Formatter
	minLevel  logrus.Level
}

func (hook *writerHook) Fire(entry *logrus.Entry) error {
	if entry.Level < hook.minLevel {
		return nil
	}
	line, err := hook.Formatter.Format(entry)
	if err != nil {
		return err
	}
	_, err = hook.Writer.Write(line)
	return err
}

func (hook *writerHook) Levels() []logrus.Level {
	return hook.LogLevels
}

// CustomFormatter 自定义日志格式(带颜色)
type CustomFormatter struct {
	UseColor bool // 是否使用颜色
}

func (f *CustomFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	timestamp := entry.Time.Format("2006-01-02 15:04:05,000")
	level := strings.ToUpper(entry.Level.String())
	message := strings.TrimRight(entry.Message, "\n")

	// 根据级别设置颜色
	var levelPart string
	if f.UseColor {
		switch entry.Level {
		case logrus.DebugLevel:
			levelPart = color.BlueString(level)
		case logrus.InfoLevel:
			levelPart = color.GreenString(level)
		case logrus.WarnLevel:
			levelPart = color.YellowString(level)
		case logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel:
			levelPart = color.RedString(level)
		default:
			levelPart = level
		}
	} else {
		levelPart = level
	}

	logLine := fmt.Sprintf("%s - %s - %s\n",
		timestamp,
		levelPart,
		message)

	return []byte(logLine), nil
}

func NewLogger(logPath string, logSize int, useColor bool) *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel) // 设置记录最低级别

	// 控制台使用带颜色的格式
	consoleFormatter := &CustomFormatter{UseColor: useColor}
	log.SetFormatter(consoleFormatter)

	// 1. 控制台输出 - 所有级别
	log.SetOutput(os.Stdout)

	// 2. 文件输出 - 仅Warning及以上级别，按大小轮转
	fileLogger := &lumberjack.Logger{
		Filename:   logPath, // 日志文件路径
		MaxSize:    logSize, // 每个日志文件最大1MB
		MaxBackups: 7,       // 保留7个备份
		MaxAge:     7,       // 保留7天
		Compress:   false,   // 不压缩备份
		LocalTime:  true,    // 使用本地时间
	}

	// 文件使用不带颜色的格式
	fileFormatter := &CustomFormatter{UseColor: false}

	// 使用Hook实现文件只记录Warning及以上级别
	log.AddHook(&writerHook{
		Writer:    fileLogger,
		LogLevels: logrus.AllLevels,
		Formatter: fileFormatter, // 使用相同的格式但不带颜色
		minLevel:  logrus.FatalLevel,
	})
	return log
}
