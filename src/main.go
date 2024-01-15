package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"os"
	"os/signal"

	"syscall"

	openaiezgo "github.com/Nigh/openai-ezgo"
	"github.com/bwmarrin/discordgo"
	"github.com/spf13/viper"

	openai "github.com/sashabaranov/go-openai"
)

var botID string
var baseurl string
var tokenLimiter int

var busyChannel map[string]bool

type ChannelConfig struct {
	ID             string `json:"id"`
	One2One        bool   `json:"one2one"`        // 一对一服务模式(必须At机器人以启动对话)
	MaxServiceTime int    `json:"maxServiceTime"` // 最大服务时间-秒
	MaxToken       int    `json:"maxToken"`       // 单次回复最大token

	enabled            bool
	currentServiceTime int    // 当前服务时间-秒
	currentServiceUser string // 当前服务用户
}

const version string = "build 001"

var oldVersion string

var GSession *discordgo.Session

var channelSettings []ChannelConfig

var gptToken string
var botToken string

func init() {
	busyChannel = make(map[string]bool)
	channelSettings = make([]ChannelConfig, 0)

	viper.SetDefault("oldversion", "unknown")
	viper.SetDefault("gpttokenmax", 512)
	viper.SetDefault("gpttoken", "0")
	viper.SetDefault("token", "0")
	viper.SetDefault("baseurl", openai.DefaultConfig("").BaseURL)
	viper.SetDefault("channels", []ChannelConfig{})
	viper.SetConfigType("json")
	viper.SetConfigName("config")
	viper.AddConfigPath("../config")
	err := viper.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("fatal error config file: %s", err))
	}

	botToken = viper.Get("token").(string)
	fmt.Println("botToken=" + botToken)
	gptToken = viper.Get("gpttoken").(string)
	fmt.Println("gptToken=" + gptToken)
	tokenLimiter = viper.GetInt("gpttokenmax")
	fmt.Println("gpttokenmax=" + strconv.Itoa(tokenLimiter))
	baseurl = viper.Get("baseurl").(string)
	fmt.Println("baseurl=" + baseurl)

	oldVersion = viper.Get("oldversion").(string)

	if err := viper.UnmarshalKey("channels", &channelSettings); err != nil {
		panic(err)
	}

	for idx := range channelSettings {
		busyChannel[channelSettings[idx].ID] = false
		channelSettings[idx].enabled = true
		if channelSettings[idx].MaxToken == 0 {
			channelSettings[idx].MaxToken = tokenLimiter
		}
	}
	for idx, v := range channelSettings {
		fmt.Printf("------ channel[%d] ------\n", idx)
		fmt.Println("\tID=" + v.ID)
		fmt.Println("\tOne2One=" + strconv.FormatBool(v.One2One))
		fmt.Println("\tMaxServiceTime=" + strconv.Itoa(v.MaxServiceTime))
		fmt.Println("\tMaxToken=" + strconv.Itoa(v.MaxToken))
	}
	go serviceTimeoutTimer()
}

func chatStatuReset(target string) {
	for idx := range channelSettings {
		if channelSettings[idx].ID == target {
			channelSettings[idx].currentServiceUser = ""
			channelSettings[idx].currentServiceTime = 0
		}
	}
}

func serviceTimeoutTimer() {
	timer := time.NewTicker(1 * time.Second)
	for range timer.C {
		for k, v := range channelSettings {
			if v.One2One && v.currentServiceUser != "" {
				channelSettings[k].currentServiceTime += 1
				// 超时结束对话
				if !busyChannel[v.ID] {
					if channelSettings[k].currentServiceTime > channelSettings[k].MaxServiceTime {
						chatStatuReset(v.ID)
						_, err := GSession.ChannelMessageSend(v.ID, openaiezgo.EndSpeech(v.ID))
						if err != nil {
							fmt.Println("[ERROR]while trying to send MSG")
						}
					}
				}
			}
		}
	}
}

func main() {
	dg, err := discordgo.New("Bot " + botToken)
	if err != nil {
		fmt.Println("error creating Discord session,", err)
		return
	}

	dg.AddHandler(ready)
	dg.AddHandler(messageCreate)
	dg.Identify.Intents = discordgo.IntentsGuildMessages

	err = dg.Open()
	if err != nil {
		fmt.Println("error opening connection,", err)
		return
	}
	botID = dg.State.User.ID
	GSession = dg

	cfg := openaiezgo.DefaultConfig(gptToken)
	cfg.BaseURL = baseurl
	cfg.MaxTokens = tokenLimiter
	cfg.TimeoutCallback = func(from string, token int) {
		chatStatuReset(from)
		dg.ChannelMessageSend(from, "连续对话已超时结束。共消耗token:`"+strconv.Itoa(token)+"`")
	}
	openaiezgo.NewClientWithConfig(cfg)

	fmt.Println("Bot online")
	if oldVersion != version {
		// dg.ChannelMessageSend(debug_channel, "升级成功！\n旧版本:"+oldVersion+"\n当前版本:"+version)
		fmt.Println("升级成功！\n旧版本:" + oldVersion + "\n当前版本:" + version)
		viper.Set("oldversion", oldVersion)
		viper.WriteConfig()
	}

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	fmt.Println("offline.")
	// dg.ChannelMessageSend(debug_channel, "Bot OFFLINE")
	dg.Close()
}

func ready(s *discordgo.Session, event *discordgo.Ready) {
	// s.ChannelMessageSend(debug_channel, "Bot ONLINE")
}

func instruction(one2one bool) (inst string) {
	inst = ""
	if one2one {
		inst += "At我即可开启一对一对话服务。\n"
	}
	inst += "命令说明：\n1. 发送 `调教 + 人格设定` 可以为我设置接下来聊天的人格。\n2. 发送 `结束对话` 可以立即终止对话服务。\n3. 发送 `帮助` 可以查看帮助信息。"
	return
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}
	if m.Author.Bot {
		return
	}
	var validChannel int = -1
	for idx, v := range channelSettings {
		if m.ChannelID == v.ID {
			validChannel = idx
			break
		}
	}
	if validChannel < 0 {
		return
	}
	// 无效频道
	if !channelSettings[validChannel].enabled {
		return
	}

	reply := func(words string) string {
		resp, err := s.ChannelMessageSend(m.ChannelID, words)
		if err != nil {
			fmt.Println("[ERROR]while trying to send msg:", words)
			return ""
		}
		return resp.ID
	}
	words := strings.TrimSpace(m.Content)
	if len(words) == 0 {
		return
	}
	regexpChat := func(words string) (ret int) {
		ret = 1
		help := regexp.MustCompile(`帮助.*`)
		if help.MatchString(words) {
			s.MessageReactionAdd(m.ChannelID, m.ID, "✅")
			if channelSettings[validChannel].One2One {
				reply(instruction(true))
			} else {
				reply(instruction(false))
			}
			return
		}
		end := regexp.MustCompile(`结束对话.*`)
		if end.MatchString(words) {
			s.MessageReactionAdd(m.ChannelID, m.ID, "✅")
			chatStatuReset(m.ChannelID)
			reply(openaiezgo.EndSpeech(m.ChannelID))
			return
		}
		cmd := regexp.MustCompile(`调教\s*(.*)`)
		if cmd.MatchString(words) {
			s.MessageReactionAdd(m.ChannelID, m.ID, "✅")
			reply(openaiezgo.NewCharacterSet(m.ChannelID, cmd.FindStringSubmatch(words)[1]))
			return
		}
		ret = 0
		return
	}
	aiChat := func(words string) {
		if busyChannel[m.ChannelID] {
			reply("正在思考中。。。请稍等。。。")
			return
		}
		busyChannel[m.ChannelID] = true
		defer func() {
			delete(busyChannel, m.ChannelID)
		}()
		s.MessageReactionAdd(m.ChannelID, m.ID, "⏳")
		ans := openaiezgo.NewSpeech(m.ChannelID, words)
		if len(ans) > 0 {
			reply(ans)
		}
		s.MessageReactionRemove(m.ChannelID, m.ID, "⏳", botID)
		s.MessageReactionAdd(m.ChannelID, m.ID, "✅")
	}

	if channelSettings[validChannel].One2One {
		// 一对一服务模式
		if channelSettings[validChannel].currentServiceUser != "" {
			// 服务中
			if m.Author.ID != channelSettings[validChannel].currentServiceUser {
				return
			} else {
				// 通用处理
				if regexpChat(words) > 0 {
					return
				}
				// 响应对话
				aiChat(words)
			}
		} else {
			// 空闲中 at启动
			for _, user := range m.Mentions {
				if user.ID == botID {
					channelSettings[validChannel].currentServiceUser = m.Author.ID
					channelSettings[validChannel].currentServiceTime = 0
					reply("接受到用户 `" + m.Author.Username + "` 的对话请求，进入一对一服务模式。从现在开始直到服务结束前，我都只会听你一个人的话。\n\n" + instruction(false))
					return
				}
			}
			// 通用处理
			if regexpChat(words) > 0 {
				return
			}
		}
	} else {
		// 通用处理
		if regexpChat(words) > 0 {
			return
		}
		// 响应对话
		aiChat(words)
	}
}
