package main

import (
	"context"
	"crypto/md5"
	cm "download/common"
	dm "download/db"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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
	gid                  int64
	export               bool
	startMsgID           int
	endMsgID             int
	maxRetry             int
	perSize              int
	topicID              int
)

func init() {
	flag.StringVar(&username, "name", "", "目标群频名")
	flag.Int64Var(&gid, "id", 0, "群ID，常用于私密群")
	flag.BoolVar(&export, "export", false, "是否导出该会话的群频信息true/false")
	flag.IntVar(&startMsgID, "s", 1, "从哪条消息ID开始处理，需>=1，从该条消息开始采集")
	flag.IntVar(&endMsgID, "e", 0, "处理到哪条消息ID，默认0不限制，不会采集该条消息")
	flag.IntVar(&maxRetry, "t", 5, "遍历聊天和下载文件出现异常的重试次数")
	flag.IntVar(&perSize, "p", 3, "每次请求多少条聊天1-100，若下载文件大，建议设小，减少文件引用过期情况")
	flag.IntVar(&topicID, "topic", -1, "采集聊天中指定话题里的消息")
	flag.Parse()

	if !export && gid == 0 && username == "" {
		panic(fmt.Errorf("运行download.exe --help查看使用方法，注意同时只能支持一个参数！"))
	}
	// 加载配置
	config = new(cm.Config)
	if err := cm.LoadConfig(config, "./conf.ini"); err != nil {
		panic(err)
	}

	// 创建目录（包括所有必要的父目录）
	err := os.MkdirAll(config.Download.SessionDir, 0644) // 0644是目录权限
	if err != nil {
		// 处理错误（通常只有权限问题才会导致错误）
		panic(fmt.Sprintf("创建session目录失败|%s|%v", config.Download.SessionDir, err))
	}

	err = os.MkdirAll(config.Download.DataDir, 0644) // 0644是目录权限
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

type GroupInfo struct {
	ID    int64
	Name  string
	Title string
	Count int
	Hash  int64
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

func getDialogs(ctx context.Context, client *telegram.Client) map[int64]*GroupInfo {
	offsetDate := 0
	limit := 100
	mp := make(map[int64]*GroupInfo)
	for {
		resp, err := client.API().MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: offsetDate,
			Limit:      limit,
			OffsetPeer: &tg.InputPeerEmpty{},
		})
		if err != nil {
			panic(fmt.Sprintf("遍历对话框失败: %s", err.Error()))
		}
		if resp == nil {
			break
		}
		if rsp, ok := resp.(*tg.MessagesDialogs); ok {
			for _, msg := range rsp.Messages {
				switch a := msg.(type) {
				case *tg.Message:
					offsetDate = a.Date
				case *tg.MessageService:
					offsetDate = a.Date
				}
			}

			for _, c := range rsp.Chats {
				if chat, ok := c.(*tg.Channel); ok {
					mp[chat.ID] = &GroupInfo{
						ID:    chat.ID,
						Name:  chat.Username,
						Title: chat.Title,
						Count: chat.ParticipantsCount,
						Hash:  chat.AccessHash,
					}
				}
			}
			if len(rsp.Dialogs) < limit {
				break
			}
		} else if rsp, ok := resp.(*tg.MessagesDialogsSlice); ok {
			for _, msg := range rsp.Messages {
				switch a := msg.(type) {
				case *tg.Message:
					offsetDate = a.Date
				case *tg.MessageService:
					offsetDate = a.Date
				}
			}

			for _, c := range rsp.Chats {
				if chat, ok := c.(*tg.Channel); ok {
					mp[chat.ID] = &GroupInfo{
						ID:    chat.ID,
						Name:  chat.Username,
						Title: chat.Title,
						Count: chat.ParticipantsCount,
						Hash:  chat.AccessHash,
					}
				}
			}
			if len(rsp.Dialogs) < limit {
				break
			}
		}
		time.Sleep(time.Second * 5)
	}
	return mp
}

//func getDialogs(ctx context.Context, client *telegram.Client) map[int64]*GroupInfo {
//	offsetID := 0
//	offsetDate := 0
//	limit := 50
//	mp := make(map[int64]*GroupInfo)
//	var x int
//	//var offsetPeer *tg.InputPeerClass
//	offsetPeer := tg.InputPeerClass(&tg.InputPeerEmpty{})
//	for {
//		fmt.Println("请求", offsetPeer, offsetDate, limit, len(mp))
//		resp, err := client.API().MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
//			//OffsetID:   offsetID,
//			OffsetDate: offsetDate,
//			Limit:      limit,
//			OffsetPeer: &tg.InputPeerEmpty{},
//		})
//		if err != nil {
//			panic(fmt.Sprintf("遍历对话框失败: %s", err.Error()))
//		}
//		if resp == nil {
//			break
//		}
//		if rsp, ok := resp.(*tg.MessagesDialogs); ok {
//			for _, do := range rsp.Dialogs {
//				offsetID = do.GetTopMessage()
//				//offsetPeer = do.GetPeer()
//				if dia, ok := do.(*tg.Dialog); ok {
//					// 获取最后一个对话的 Peer
//					switch peer := dia.Peer.(type) {
//					case *tg.PeerUser:
//						offsetPeer = &tg.InputPeerUser{UserID: peer.UserID}
//					case *tg.PeerChat:
//						offsetPeer = &tg.InputPeerChat{ChatID: peer.ChatID}
//					case *tg.PeerChannel:
//						offsetPeer = &tg.InputPeerChannel{ChannelID: peer.ChannelID}
//					}
//				}
//			}
//
//			for _, msg := range rsp.Messages {
//				switch a := msg.(type) {
//				case *tg.Message:
//					offsetDate = a.Date
//				case *tg.MessageService:
//					offsetDate = a.Date
//				}
//			}
//
//			for _, c := range rsp.Chats {
//				if chat, ok := c.(*tg.Channel); ok {
//					mp[chat.ID] = &GroupInfo{
//						ID:    chat.ID,
//						Name:  chat.Username,
//						Title: chat.Title,
//						Count: chat.ParticipantsCount,
//						Hash:  chat.AccessHash,
//					}
//					x++
//					fmt.Println("aaa", x, len(mp), len(rsp.Dialogs))
//				}
//			}
//			if len(rsp.Dialogs) < limit {
//				break
//			}
//		} else if rsp, ok := resp.(*tg.MessagesDialogsSlice); ok {
//			for _, do := range rsp.Dialogs {
//				offsetID = do.GetTopMessage()
//				fmt.Println("id", offsetID)
//				if dia, ok := do.(*tg.Dialog); ok {
//					// 获取最后一个对话的 Peer
//					switch peer := dia.Peer.(type) {
//					case *tg.PeerUser:
//						offsetPeer = &tg.InputPeerUser{UserID: peer.UserID}
//					case *tg.PeerChat:
//						offsetPeer = &tg.InputPeerChat{ChatID: peer.ChatID}
//					case *tg.PeerChannel:
//						offsetPeer = &tg.InputPeerChannel{ChannelID: peer.ChannelID}
//					}
//				}
//			}
//
//			for _, msg := range rsp.Messages {
//				switch a := msg.(type) {
//				case *tg.Message:
//					offsetDate = a.Date
//				case *tg.MessageService:
//					offsetDate = a.Date
//				}
//			}
//
//			for _, c := range rsp.Chats {
//				if chat, ok := c.(*tg.Channel); ok {
//					mp[chat.ID] = &GroupInfo{
//						ID:    chat.ID,
//						Name:  chat.Username,
//						Title: chat.Title,
//						Count: chat.ParticipantsCount,
//						Hash:  chat.AccessHash,
//					}
//					x++
//					fmt.Println("bbb", x, len(mp), len(rsp.Dialogs))
//				}
//			}
//			if len(rsp.Dialogs) < limit {
//				break
//			}
//		}
//		time.Sleep(time.Second * 3)
//	}
//	return mp
//}

func getFileMd5(s []byte) string {
	hasher := md5.New()
	hasher.Write(s)
	md5Str2 := hex.EncodeToString(hasher.Sum(nil))
	return md5Str2
}

func getGroupInfo(ctx context.Context, client *telegram.Client) *tg.InputChannel {
	resolved, err := client.API().ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		logger.Errorf("failed to resolve username: %s|%s", username, err.Error())
		var rpcErr *tgerr.Error
		if errors.As(err, &rpcErr) {
			if rpcErr.Code == 400 {
				logger.Errorf("无效名：%s", username)
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
		logger.Errorf("%s非群频: %T", username, peer)
	}

	return nil
}

func getGroupMessage(ctx context.Context, client *telegram.Client) {
	var peer *tg.InputPeerChannel
	if username != "" {
		group := getGroupInfo(ctx, client)
		if group == nil {
			return
		}
		peer = &tg.InputPeerChannel{
			ChannelID:  group.ChannelID,
			AccessHash: group.AccessHash,
		}
		gid = group.ChannelID
		logger.Infof("正在处理群频：%s", username)
	} else if gid != 0 {
		groupData := getDialogs(ctx, client)
		if info, ok := groupData[gid]; ok {
			peer = &tg.InputPeerChannel{
				ChannelID:  gid,
				AccessHash: info.Hash,
			}
			logger.Infof("正在处理群频：%d", gid)
		} else {
			logger.Errorf("会话%s找不到群频：%d", session, gid)
			return
		}
	}

	// 获取聊天历史记录
	offset := startMsgID
	logger.Infof("当前从消息ID：%d开始采集", offset)
	var mtries int
loop:
	for {
		// 调用 ChannelsGetHistory 方法
		var history tg.MessagesMessagesClass
		var err error
		if topicID != -1 {
			history, err = client.API().MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{
				Peer:       peer,
				MsgID:      topicID,
				OffsetID:   offset,   // 从最新消息开始
				OffsetDate: 0,        // 不需要按日期偏移
				AddOffset:  -perSize, // 设置偏移量
				Limit:      perSize,  // 每次获取的消息数量
				MaxID:      0,        // 最大消息 ID（0 表示不限制）
				MinID:      0,        // 最小消息 ID（0 表示不限制）
				Hash:       0,
			})
		} else {
			history, err = client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
				Peer:       peer,
				OffsetID:   offset,   // 从最新消息开始
				OffsetDate: 0,        // 不需要按日期偏移
				AddOffset:  -perSize, // 设置偏移量
				Limit:      perSize,  // 每次获取的消息数量
				MaxID:      0,        // 最大消息 ID（0 表示不限制）
				MinID:      0,        // 最小消息 ID（0 表示不限制）
				Hash:       0,
			})
		}

		if err != nil {
			if mtries >= maxRetry {
				logger.Errorf("遍历消息【%s:%d】多次失败，结束", username, offset)
				break
			}
			var rpcErr *tgerr.Error
			if errors.As(err, &rpcErr) {
				if rpcErr.Code == 420 {
					logger.Infof("遍历消息需要等待|%d|waitting...", rpcErr.Argument)
					time.Sleep(time.Second * time.Duration(rpcErr.Argument+2))
					client.Self(ctx)
				} else {
					time.Sleep(time.Second * 1)
					logger.Warningf("遍历消息失败：%d|%s，重试中... (%d/%d)", rpcErr.Code, err.Error(), mtries+1, maxRetry)
				}
			} else {
				time.Sleep(time.Second * 1)
				logger.Warningf("遍历消息失败：%s，重试中... (%d/%d)", err.Error(), mtries+1, maxRetry)
			}
			mtries++
			continue
		}
		mtries = 0

		// 处理返回的消息
		var id int
		switch resp := history.(type) {
		case *tg.MessagesChannelMessages:
			if len(resp.Messages) == 0 {
				logger.Infof("群频：%s已处理完成", username)
				break loop
			}
			reverse(resp.Messages)
			for _, msg := range resp.Messages {
				switch tgMsg := msg.(type) {
				case *tg.Message:
					tm := time.Unix(int64(tgMsg.Date), 0).Format(time.DateTime)
					id = tgMsg.ID
					if endMsgID > 0 && id >= endMsgID {
						logger.Infof("已处理到目标消息ID：%d", endMsgID)
						break loop
					}
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
									fileName = fmt.Sprintf("%d---%d---.%s", gid, id, xlis[len(xlis)-1])
								} else {
									fid = getFileMd5([]byte(fmt.Sprintf("%s%d", fileName, docu.Size)))
									fileName = fmt.Sprintf("%d---%d---.unknown", gid, id)
								}

							} else {
								tgFileName = fileName
								fid = getFileMd5([]byte(fmt.Sprintf("%s%d", fileName, docu.Size)))
								fileName = fmt.Sprintf("%d---%d---%s", gid, id, fileName)
							}

							suffixLis := strings.Split(fileName, ".")
							if len(suffixLis) >= 2 {
								s := suffixLis[len(suffixLis)-1]
								if len(config.Download.Dtypes) > 0 {
									// 下载文件类型过滤
									if _, ok := config.Download.Dtypes[s]; !ok {
										logger.Infof("过滤%s类型文件%.2fMB", s, mSize)
										continue
									}
								}
								if _, ok := config.Download.EDtypes[s]; ok {
									// 过滤不下载的文件类型
									logger.Infof("过滤%s类型文件%.2fMB", s, mSize)
									continue
								}
							}

							if dm.DbIsFileExists(logger, fid) {
								logger.Infof("已在数据库找到文件记录，跳过：【%s】", fileName)
								continue
							}

							var lastSize int64
							var isBlank, isOK bool
							filePath := filepath.Join(config.Download.DataDir, fileName)
							ff, _ := os.Stat(filePath)

							if ff != nil {
								// 存在文件
								lastSize = ff.Size()
								if lastSize == docu.Size {
									logger.Infof("本地文件已存在，且大小相同，跳过：【%s】", fileName)
									isOK = true
								} else if lastSize%4096 != 0 {
									lastSize = 0
									logger.Infof("群频%s第%d消息，原文件已损坏，无法继续下载文件，开始重新下载：【%s】", username, id, fileName)
									err = os.Remove(filePath)
									if err != nil {
										logger.Errorf("删除旧文件【%s】失败：%s", filePath, err.Error())
										continue
									} else {
										isBlank = true
										logger.Infof("已删除损坏文件：【%s】", filePath)
									}
								} else {
									rate := float64(lastSize) / float64(docu.Size) * 100
									logger.Infof("群频%s第%d消息，原文件进度%.2f%%，正在继续下载：【%s】", username, id, rate, fileName)
								}
							} else {
								// 不存在文件
								isBlank = true
								logger.Infof("正在下载群频%s第%d消息文件：【%s】 大小：【%.2fMB】", username, id, fileName, mSize)
							}

							if !isOK {
								of, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
								if err != nil {
									logger.Errorf("创建文件失败：【%s】|%v", filePath, err)
									continue
								}

								var c, retries int
								for {
									a, err := client.API().UploadGetFile(ctx, &tg.UploadGetFileRequest{
										Location: docu.AsInputDocumentFileLocation(),
										Offset:   lastSize,
										Limit:    1024 * 1024, // 每次处理1MB
									})

									if err != nil {
										if retries >= maxRetry {
											logger.Errorf("下载群频%s第%d消息文件：【%s】多次失败，已跳过", username, id, fileName)
											break
										}
										var rpcErr *tgerr.Error
										if errors.As(err, &rpcErr) {
											if rpcErr.Code == 420 {
												logger.Warningf("下载资源需要等待|%d|waitting...", rpcErr.Argument)
												time.Sleep(time.Second * time.Duration(rpcErr.Argument+2))
												client.Self(ctx)
											} else {
												time.Sleep(time.Second * 1)
												logger.Warningf("下载失败：%s，重试中... (%d/%d)", err.Error(), retries+1, maxRetry)
											}
										} else {
											time.Sleep(time.Second * 1)
											logger.Warningf("下载失败：%s，重试中... (%d/%d)", err.Error(), retries+1, maxRetry)
										}
										retries++
										continue
									}
									retries = 0

									if b, ok := a.(*tg.UploadFile); ok {
										lastSize += int64(len(b.Bytes))
										_, err = of.Write(b.Bytes)
										c++
										if err != nil {
											logger.Errorf("下载过程中写入文件失败:%v", err)
											break
										}
										if c%100 == 0 {
											err = of.Sync()
											if err != nil {
												logger.Errorf("文件写入同步失败:%v", err)
												break
											}
										}
										cm.ProgressBar(lastSize, docu.Size)
										if lastSize >= docu.Size {
											isOK = true
											break
										}
									} else {
										logger.Infof("下载过程中类型异常：%v\n", reflect.TypeOf(a))
										break
									}
								}
								err = of.Sync()
								if err != nil {
									logger.Errorf("文件最终写入同步失败:%v", err)
									break
								}
								of.Close()
								if isBlank && c == 0 {
									// 新建的文件没有任何写入，失败了，则删除临时生成的文件
									err = os.Remove(filePath)
									if err != nil {
										logger.Warningf("删除临时文件【%s】失败：%s", filePath, err.Error())
									} else {
										logger.Infof("已删除临时文件：【%s】", filePath)
									}
								}
							}

							if isOK {
								file := dm.TgFile{
									Fid:   fid,
									Gid:   gid,
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
					if endMsgID > 0 && id >= endMsgID {
						logger.Infof("已处理到目标消息ID：%d", endMsgID)
						break loop
					}
					//fmt.Println(id, tm, "service")
				case *tg.MessageEmpty:
					id = tgMsg.ID
					if endMsgID > 0 && id >= endMsgID {
						logger.Infof("已处理到目标消息ID：%d", endMsgID)
						break loop
					}
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

func exportData(ctx context.Context, client *telegram.Client) {
	groupData := getDialogs(ctx, client)

	if len(groupData) > 0 {
		fpath := fmt.Sprintf("./%s_groups.csv", session)
		ff, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			panic(err)
		}
		defer ff.Close()
		cv := csv.NewWriter(ff)
		cv.UseCRLF = true
		cv.Write([]string{"ID", "Name", "Title", "Count"})
		for _, group := range groupData {
			cv.Write([]string{strconv.Itoa(int(group.ID)), group.Name, group.Title, strconv.Itoa(int(group.Count))})
		}
		cv.Flush()
		fmt.Printf("已导出%d个群频信息>>>%s\n", len(groupData), fpath)
	} else {
		fmt.Println("未找到用户对话框")
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
		logger.Errorf("无可用会话，请导入会话到会话目录！")
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

		if export {
			exportData(ctx, client)
			return nil
		}

		//username := "BabukLockerRaas" // 替换为你要查找的群组用户名
		//username := "jjifei" // 替换为你要查找的群组用户名
		getGroupMessage(ctx, client)

		return nil
	}); err != nil {
		logger.Infof("运行异常：%v", err)
		var rpcErr *tgerr.Error
		if errors.As(err, &rpcErr) {
			if rpcErr.Code == 401 {
				logger.Warningf("账号失效，已移除：%s", sessionPath)
				cleanSession()
			}
		}
	}
}

// 2025-04-10 22:43:03,283 - INFO - 正在下载群频BabukLockerRaas第319消息文件：【2305362783---319---armetal.com2.zip】 大小：【284.5MB】
