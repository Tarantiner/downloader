package common

import (
	"fmt"
	"strings"
)

// ProgressBar 显示进度条
func ProgressBar(currentSize, totalSize int64) {
	const barWidth = 50 // 进度条宽度（字符数）

	// 计算进度百分比
	percent := float64(currentSize) / float64(totalSize) * 100
	if percent > 100 {
		percent = 100
	}

	// 计算已完成部分的字符数
	filled := int(float64(barWidth) * percent / 100)
	if filled > barWidth {
		filled = barWidth
	}

	// 构建进度条
	bar := strings.Repeat("█", filled) + strings.Repeat("-", barWidth-filled)

	// 格式化大小（转换为 MB）
	currentMB := float64(currentSize) / (1024 * 1024)
	totalMB := float64(totalSize) / (1024 * 1024)

	// 输出进度条（使用 \r 刷新同一行）
	fmt.Printf("\r[%s] %.2f%% (%.2f/%.2f MB)", bar, percent, currentMB, totalMB)

	// 如果完成，换行
	if currentSize >= totalSize {
		fmt.Println()
	}
}
