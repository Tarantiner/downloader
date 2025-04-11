package main

import (
	cm "download/common"
	dm "download/db"
	"fmt"
)

var config *cm.Config

func init() {
	// 加载配置
	config = new(cm.Config)
	if err := cm.LoadConfig(config, "./conf.ini"); err != nil {
		panic(err)
	}
	dm.DbInit(config.DB.DBPath)
}

func main() {
	fmt.Println("########################## 开启查询文件信息 ##########################")
	fmt.Println("你可以 ctrl + c to 结束")
	for {
		func() {
			var fname string
			fmt.Print("请输入文件名(如abc.txt)>>>")
			fmt.Scanln(&fname)
			if fname == "" {
				fmt.Println("输入内容为空!\n")
				return
			}
			fileLis, err := dm.DbCheckFile(fname)
			if err != nil {
				fmt.Printf("查询【%s】失败：%s\n", fname, err.Error())
				return
			}
			if len(fileLis) == 0 {
				fmt.Printf("查询【%s】结果为空\n", fname)
				return
			}
			for i, fi := range fileLis {
				size := float64(fi.Fsize) / 1024
				fmt.Printf(`##########################
查询【%s】结果%d：
群名：%s,
群id：%d
群消息id：%d
原文件名：%s
下载文件名：%s
下载路径：%s
文件大小：%.1fKB
发言时间：%s
附属消息：%s
`, fname, i+1, fi.Gname, fi.Gid, fi.Mid, fi.Fname, fi.Dname, fi.Fpath, size, fi.Ftime, fi.Msg)
			}
		}()
	}
}
