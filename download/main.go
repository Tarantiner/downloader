package main

import (
	"context"
	"crypto/md5"
	cm "download/common"
	dm "download/db"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	logger               *logrus.Logger
	config               *cm.Config
	resolver             dcs.Resolver
	session, sessionPath string
	ctx                  = context.Background()
	client               *telegram.Client
	username             string
)

func init() {
	flag.StringVar(&username, "name", "", "目标群频名")
	flag.Parse()
	if username == "" {
		panic(fmt.Errorf("群频名为空"))
	}
	// 加载配置
	config = new(cm.Config)
	if err := cm.LoadConfig(config, "./conf.ini"); err != nil {
		panic(err)
	}

	// 创建目录（包括所有必要的父目录）
	err := os.MkdirAll(config.Download.SessionDir, 0755) // 0755是目录权限
	if err != nil {
		// 处理错误（通常只有权限问题才会导致错误）
		panic(fmt.Sprintf("创建session目录失败|%s|%v", config.Download.SessionDir, err))
	}

	err = os.MkdirAll(config.Download.DataDir, 0755) // 0755是目录权限
	if err != nil {
		// 处理错误（通常只有权限问题才会导致错误）
		panic(fmt.Sprintf("创建下载目录失败|%s|%v", config.Download.DataDir, err))
	}

	// 初始化日志
	logger = cm.NewLogger("./log/download.log", config.Common.LogSplitSize, true)
	// 初始化网络
	if config.NET.UseProxy {
		err := MakeResolver()
		if err != nil {
			panic(err)
		}
	}

	dm.DbInit(config.DB.DBPath)
}

type FileSessionStorage struct {
	FilePath string
}

func (f FileSessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	data, err := os.ReadFile(f.FilePath)
	if os.IsNotExist(err) {
		return nil, nil // 文件不存在时返回空
	}
	return data, err
}

func (f FileSessionStorage) StoreSession(ctx context.Context, data []byte) error {
	return os.WriteFile(f.FilePath, data, 0644)
}

func NewDialer(proxyConnStr string) (proxy.Dialer, error) {
	diaUrl, err := url.Parse(proxyConnStr)
	if err != nil {
		return nil, err
	}
	socks5, err := proxy.FromURL(diaUrl, proxy.Direct)
	if err != nil {
		return nil, err
	}
	return socks5, nil
}

func MakeResolver() error {
	socks5, err := NewDialer(fmt.Sprintf("socks5://%s:%d", config.NET.ProxyHost, config.NET.ProxyPort))
	if err != nil {
		return err
	}
	dc := socks5.(proxy.ContextDialer)
	resolver = dcs.Plain(dcs.PlainOptions{
		Dial: dc.DialContext,
	})
	return nil
}

func reverse(s []tg.MessageClass) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func getFileMd5(s []byte) string {
	hasher := md5.New()
	hasher.Write(s)
	md5Str2 := hex.EncodeToString(hasher.Sum(nil))
	return md5Str2
}

func getGroupInfo(ctx context.Context, client *telegram.Client, username string) *tg.InputChannel {
	resolved, err := client.API().ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		logger.Warningf("failed to resolve username: %s", err.Error())
		var rpcErr *tgerr.Error
		if errors.As(err, &rpcErr) {
			// 检查错误码是否为 420 (FLOOD_WAIT)
			if rpcErr.Code == 400 {
				logger.Warningf("无效名：%s", username)
			}
		}
		return nil
	}

	// 检查解析结果是否为群组
	peer := resolved.GetPeer()
	switch peer := peer.(type) {
	case *tg.PeerChannel: // 处理频道/群组
		group := &tg.InputChannel{
			ChannelID:  peer.ChannelID,
			AccessHash: resolved.GetChats()[0].(*tg.Channel).AccessHash, // 从解析结果中获取 AccessHash
		}
		return group
	default:
		logger.Warningf("%s非群频: %T", username, peer)
	}

	return nil
}

func getGroupMessage(ctx context.Context, client *telegram.Client, username string) {
	logger.Infof("正在处理群频：%s", username)
	group := getGroupInfo(ctx, client, username)
	if group == nil {
		return
	}
	// 获取聊天历史记录
	//chatID := group.ChannelID // 替换为目标聊天 ID
	offset := 1 // 从第 20 条消息开始
	limit := 3  // 偏移量更小，减少文件引用失效情况

	peer := &tg.InputPeerChannel{
		ChannelID:  group.ChannelID,
		AccessHash: group.AccessHash,
	}

	var count int
loop:
	for {
		// 调用 ChannelsGetHistory 方法
		history, err := client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:       peer,
			OffsetID:   offset, // 从最新消息开始
			OffsetDate: 0,      // 不需要按日期偏移
			AddOffset:  -limit, // 设置偏移量
			Limit:      limit,  // 每次获取的消息数量
			MaxID:      0,      // 最大消息 ID（0 表示不限制）
			MinID:      0,      // 最小消息 ID（0 表示不限制）
			Hash:       0,
		})
		if err != nil {
			var rpcErr *tgerr.Error
			if errors.As(err, &rpcErr) {
				// 检查错误码是否为 420 (FLOOD_WAIT)
				if rpcErr.Code == 420 {
					logger.Infof("遍历消息需要等待|%d", rpcErr.Argument)
					time.Sleep(time.Second * time.Duration(rpcErr.Argument))
					continue
				} else {
					logger.Fatalf("Failed to get channel history: %s", err.Error())
				}
			} else {
				logger.Fatalf("Failed to get channel history: %s", err.Error())
			}
		}

		// 处理返回的消息
		var id int
		switch resp := history.(type) {
		case *tg.MessagesChannelMessages:
			if len(resp.Messages) == 0 {
				break loop
			}
			reverse(resp.Messages)
			for _, msg := range resp.Messages {
				count++
				switch tgMsg := msg.(type) {
				case *tg.Message:
					tm := time.Unix(int64(tgMsg.Date), 0).Format(time.DateTime)
					id = tgMsg.ID

					if md, ok := tgMsg.Media.(*tg.MessageMediaDocument); ok {
						if docu, ok := md.Document.(*tg.Document); ok {
							mSize := float64(docu.Size) / 1024 / 1024
							var tgFileName, fileName string
						at:
							for _, attr := range docu.Attributes {
								if fn, ok := attr.(*tg.DocumentAttributeFilename); ok {
									fileName = fn.FileName
									break at
								}
							}

							var fid string
							if fileName == "" {
								var fType string
								xlis := strings.Split(docu.MimeType, "/")
								if len(xlis) >= 2 {
									fType = xlis[len(xlis)-1]
									tgFileName = fType
									fid = getFileMd5([]byte(fmt.Sprintf("%s%d", fType, docu.Size)))
									fileName = fmt.Sprintf("%d---%d---.%s", group.ChannelID, id, xlis[len(xlis)-1])
								} else {
									fid = getFileMd5([]byte(fmt.Sprintf("%s%d", fileName, docu.Size)))
									fileName = fmt.Sprintf("%d---%d---.unknown", group.ChannelID, id)
								}

							} else {
								tgFileName = fileName
								fid = getFileMd5([]byte(fmt.Sprintf("%s%d", fileName, docu.Size)))
								fileName = fmt.Sprintf("%d---%d---%s", group.ChannelID, id, fileName)
							}

							suffixLis := strings.Split(fileName, ".")
							if len(suffixLis) >= 2 {
								s := suffixLis[len(suffixLis)-1]
								//fmt.Printf("【%s】\n", s)
								if len(config.Download.Dtypes) > 0 {
									// 下载文件类型过滤
									if _, ok := config.Download.Dtypes[s]; !ok {
										logger.Infof("过滤%s类型文件%.1fMB", s, mSize)
										continue
									}
								}
								if _, ok := config.Download.EDtypes[s]; ok {
									logger.Infof("过滤%s类型文件%.1fMB", s, mSize)
									continue
								}
							}

							if dm.DbIsFileExists(logger, fid) {
								logger.Infof("已在数据库找到文件记录，跳过：【%s】", fileName)
								continue
							}

							var toDb, exists bool
							filePath := filepath.Join(config.Download.DataDir, fileName)
							ff, _ := os.Stat(filePath)
							if ff != nil {
								rate := float64(ff.Size() / docu.Size)
								if rate >= 0.95 {
									exists = true
								} else {
									logger.Infof("本地存在的文件%s不完整：%.1f，程序重新下载", filePath, rate)
								}
							}
							if exists {
								logger.Infof("同名文件已下载过，跳过：%s", fileName)
								toDb = true
							} else {
								logger.Infof("正在下载群频%s第%d消息文件：【%s】 大小：【%.1fMB】", username, id, fileName, mSize)
								dl := downloader.NewDownloader()
								for i := 0; i < 3; i++ {
									_, err = dl.Download(client.API(), docu.AsInputDocumentFileLocation()).WithThreads(config.Download.Threads).ToPath(ctx, filePath)
									if err != nil {
										logger.Warningf("下载群频%s第%d消息文件：【%s】 大小：【%.1fMB】失败|%s", username, id, fileName, mSize, err.Error())
										logger.Infof("下载异常，正在重试%d次...", i+1)
									} else {
										toDb = true
										break
									}
								}
								if !toDb {
									os.Remove(filePath)
									logger.Infof("多次下载失败")
								}
							}
							if toDb {
								file := dm.TgFile{
									Fid:   fid,
									Gid:   group.ChannelID,
									Gname: username,
									Mid:   id,
									Fname: tgFileName,
									Dname: fileName,
									Fpath: filePath,
									Fsize: docu.Size,
									Ftime: tm,
									Msg:   tgMsg.Message,
								}
								dm.DbNewFile(logger, &file)
							}
						}
					}
				case *tg.MessageService: // 比如某人加入离开群组
					//tm := time.Unix(int64(tgMsg.Date), 0).Format(time.DateTime)
					id = tgMsg.ID
					//fmt.Println(id, tm, "service")
				case *tg.MessageEmpty:
					id = tgMsg.ID
					//fmt.Println(id)
				default:
					//fmt.Println("ddd", reflect.TypeOf(tgMsg))
					break loop
				}
			}
		default:
			logger.Fatalf("Unexpected response type: %T", history)
		}

		offset = id + 1
		time.Sleep(time.Millisecond * 800)
	}
}

func cleanSession() {
	if _, err := os.Stat(sessionPath); err == nil {
		os.Rename(sessionPath, sessionPath+".bak")
	}
}

func getSession() bool {
	// 检查路径是否存在
	fileInfo, err := os.Stat(config.Download.SessionDir)
	if err != nil {
		logger.Errorf("session路径不存在: %s", config.Download.SessionDir)
		return false
	}

	// 检查是否是目录
	if !fileInfo.IsDir() {
		logger.Errorf("session路径不是目录: %s", config.Download.SessionDir)
		return false
	}

	session, sessionPath = "", ""
	// 遍历目录查找JSON文件
	err = filepath.WalkDir(config.Download.SessionDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // 返回遍历过程中遇到的错误
		}

		// 只处理普通文件
		if !d.Type().IsRegular() {
			return nil
		}

		// 检查文件扩展名
		if filepath.Ext(path) == ".json" {
			sessionPath = path
			session = strings.TrimRight(filepath.Base(path), ".json")
			return filepath.SkipAll // 找到第一个后停止遍历
		}

		return nil
	})

	if err != nil {
		logger.Errorf("遍历session目录|%s出错: %v", config.Download.SessionDir, err)
		return false
	}

	if session != "" && sessionPath != "" {
		return true
	}
	logger.Errorf("未找到session文件: %s", config.Download.SessionDir)
	return false
}

func main() {
	if !getSession() {
		logger.Warningf("无可用会话，请导入会话到会话目录！")
		return
	}

	ctx = context.Background()

	// 文件存储路径
	storage := FileSessionStorage{FilePath: sessionPath}

	// 创建客户端并设置会话存储
	if config.NET.UseProxy {
		client = telegram.NewClient(config.Download.APIID, config.Download.APIHash, telegram.Options{
			SessionStorage: storage,
			Resolver:       resolver,
		})
	} else {
		client = telegram.NewClient(config.Download.APIID, config.Download.APIHash, telegram.Options{
			SessionStorage: storage,
		})
	}

	// 运行客户端
	if err := client.Run(ctx, func(ctx context.Context) error {
		// 测试获取当前用户信息
		self, err := client.Self(ctx)
		if err != nil {
			return err
		}
		logger.Infof("Logged in as: %s (%d)", self.Username, self.ID)

		//username := "BabukLockerRaas" // 替换为你要查找的群组用户名
		//username := "jjifei" // 替换为你要查找的群组用户名
		getGroupMessage(ctx, client, username)

		return nil
	}); err != nil {
		logger.Infof("运行异常：%v", err)
		var rpcErr *tgerr.Error
		if errors.As(err, &rpcErr) {
			// 检查错误码是否为 420 (FLOOD_WAIT)
			if rpcErr.Code == 401 {
				logger.Warningf("账号失效，已移除：%s", sessionPath)
				cleanSession()
			}
		}
	}
}

// 2025-04-10 22:43:03,283 - INFO - 正在下载群频BabukLockerRaas第319消息文件：【2305362783---319---armetal.com2.zip】 大小：【284.5MB】
