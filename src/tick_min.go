package src

import (
	"archive/tar"
	"compress/gzip"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"database/sql"

	_ "github.com/lib/pq" // postgres
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/html/charset"
)

var (
	// 交易日历
	tradingDays []string
	// postgres配置
	pgMin string
)

// mins 品种交易分钟
type mins struct {
	Opens []string
	Ends  []string
	Mins  []string
}

// Bar K线
type Bar struct {
	DateTime     string
	Open         float64
	High         float64
	Low          float64
	Close        float64
	Volume       int32
	OpenInterest float64
}

// Bars bar可排序序列
type Bars []*Bar

func (b Bars) Len() int {
	return len(b)
}
func (b Bars) Less(i, j int) bool {
	return b[i].DateTime < b[j].DateTime
}
func (b Bars) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}

func init() {
	// 变量初始化
	pgMin = ""
	if tmp := os.Getenv("pgMin"); len(tmp) > 0 {
		pgMin = tmp
		logrus.Info("postgres :", pgMin)
	} else {
		logrus.Warn("postgres 未配置")
	}

	readCalendar()
}

// readCalendar 取交易日历
func readCalendar() {
	cal, err := os.Open("calendar.csv")
	defer cal.Close()
	if err != nil {
		logrus.Error(err)
	}
	reader := csv.NewReader(cal)
	lines, err := reader.ReadAll()
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		if line[1] == "true" {
			tradingDays = append(tradingDays, line[0])
		}
	}
	sort.Strings(tradingDays)
}

// Run 根据起码日期执行
func Run(startDay string) {
	// 取最大已处理日期
	if startDay == "" {
		db, err := sql.Open("postgres", pgMin)
		if err != nil {
			logrus.Fatal("数据库打开错误", err)
			return
		}
		defer db.Close()
		rows, err := db.Query(`select max("TradingDay" ) from future.future_min`)
		if err != nil {
			logrus.Fatal("取最大交易日报错", err)
		}
		defer rows.Close()
		rows.Next()
		rows.Scan(&startDay)
		// 空库
		if len(startDay) == 0 {
			startDay = "20120813"
		}
	}

	// 取大于日期的交易日
	var days []string
	for _, day := range tradingDays {
		if day > startDay {
			days = append(days, day)
		}
	}

	for _, day := range days {
		// gzip 读取tick.csv.gz数据
		// 文件不存在,sleep 10min重读
		logrus.Infof("%s starting...", day)
		for {
			msg, err := RunOnce(day)
			if err == nil {
				break
			} else if os.IsNotExist(err) {
				time.Sleep(10 * time.Minute)
				continue
			}
			logrus.Error(msg, err)
			return
		}
	}
}

// RunOnce 处理一天数据
func RunOnce(tradingDay string) (msg string, err error) {
	if pgMin == "" {
		logrus.Error("pg未配置")
		return "pg未配置", errors.Errorf("config err")
	}
	// 取 xml
	var xmlReader io.Reader
	localFile := path.Join("/xml", tradingDay+".tar.gz")
	if xmlFile, err := os.OpenFile(localFile, os.O_RDONLY, os.ModePerm); err != nil {
		// 本地没有, 用sftp
		if os.IsNotExist(err) {
			// export xmlSftp=192.168.111.191/22/root/123456
			if tmp := os.Getenv("xmlSftp"); tmp != "" {
				ss := strings.Split(tmp, "/")
				host, user, pwd := ss[0], ss[2], ss[3]
				port, _ := strconv.Atoi(ss[1])
				var sftp *HfSftp
				if sftp, err = NewHfSftp(host, port, user, pwd); err == nil {
					defer sftp.Close()
					remoteFullName := path.Join(os.Getenv("xmlSftpPath"), tradingDay+".tar.gz")
					if sftpFile, err := sftp.client.Open(remoteFullName); err == nil {
						xmlReader = sftpFile
						logrus.Info("reading file from sftp: ", remoteFullName)
					} else {
						return "sftp 读取文件错误", err
					}
				} else {
					return "sftp 连接错误", err
				}
			} else { // 未配置 Sftp
				return "sftp 未配置", err
			}
		} else {
			return "取本地xml文件错误:", err
		}
	} else {
		xmlReader = xmlFile
		logrus.Info("reading file: ", localFile)
		// xml 解析
		_, err = xmlFile.Seek(0, 0) // 切换到文件开始,否则err==EOF
	}
	var gr *gzip.Reader
	if gr, err = gzip.NewReader(xmlReader); err == nil {
		defer gr.Close()

		tr := tar.NewReader(gr)
		// 解压tar中的所有文件(其实只有一个)
		_, _ = tr.Next()
		// 包中的 marketdata.xml 解析成tick并入库
		decoder := xml.NewDecoder(tr)
		// 处理汉字编码
		decoder.CharsetReader = func(c string, i io.Reader) (io.Reader, error) {
			return charset.NewReaderLabel(strings.TrimSpace(c), i)
		}
		// 处理actionday
		var actionDay, actionNextDay string
		if len(tradingDays) == 1 {
			actionDay = tradingDay
			actionNextDay = tradingDay
		} else {
			idx := -1
			for i := 0; i < len(tradingDays); i++ {
				if tradingDays[i] == tradingDay {
					idx = i
					break
				}
			}
			if idx == -1 {
				return tradingDay + " is not in trading calendaer.", os.ErrNotExist
			}
			actionDay = tradingDays[idx-1]
			t, _ := time.Parse("20060102", actionDay)
			actionNextDay = t.AddDate(0, 0, 1).Format("20060102")
		}

		// 合约与分钟K线
		instBars := make(map[string]Bars, 0)
		minTicks := make(map[string]int, 0)
		// 读取 tick
		cnt := 0
		for t, err := decoder.Token(); err == nil; t, err = decoder.Token() {
			if t == nil {
				logrus.Error("get token error!")
				break
			}

			switch start := t.(type) {
			case xml.StartElement:
				// ...and its name is "page" NtfDepthMarketDataPackage
				se := start.Copy()
				// ee := se.End()
				if se.Name.Local == "NtfDepthMarketDataPackage" {
					p := NtfDepthMarketDataPackage{}
					if err = decoder.DecodeElement(&p, &se); err != nil {
						return "解析xml错误", err
					}
					cnt++
					if cnt%500000 == 0 {
						logrus.Info(cnt)
					}
					// 过虑脏数据
					if p.MarketDataLastMatchField.Volume == 0 || p.MarketDataLastMatchField.LastPrice == 0 || p.MarketDataBestPriceField.AskPrice1 == 0 || p.MarketDataBestPriceField.BidPrice1 == 0 {
						continue
					}
					// 处理actionDay
					if hour, err := strconv.Atoi(p.MarketDataUpdateTimeField.UpdateTime[0:2]); err == nil {
						if hour >= 20 {
							p.MarketDataUpdateTimeField.ActionDay = actionDay
						} else if hour < 4 {
							p.MarketDataUpdateTimeField.ActionDay = actionNextDay
						} else {
							p.MarketDataUpdateTimeField.ActionDay = tradingDay
						}
					} else {
						return "解析tick错误", err
					}

					// 数据处理
					InstrumentID := p.MarketDataUpdateTimeField.InstrumentID
					UpdateTime := p.MarketDataUpdateTimeField.UpdateTime
					ActionDay := p.MarketDataUpdateTimeField.ActionDay
					last := float64(p.MarketDataLastMatchField.LastPrice)
					volume := p.MarketDataLastMatchField.Volume
					oi := float64(p.MarketDataLastMatchField.OpenInterest)

					minTime := UpdateTime[0:6] + "00"
					// 取合约对应的bars
					bars, ok := instBars[InstrumentID]
					if !ok {
						bars = Bars{}
						instBars[InstrumentID] = bars
					}
					// 取当前 bar
					curBar := &Bar{}
					if len(bars) > 0 {
						curBar = bars[len(bars)-1]
					}
					// 当前 日期+时间 yyyyMMddHH:mm:00
					curDt := ActionDay + minTime
					// 新 bar
					if curBar.DateTime != curDt {
						curBar = &Bar{
							DateTime:     curDt,
							Open:         last,
							High:         last,
							Low:          last,
							Close:        last,
							Volume:       volume,
							OpenInterest: oi,
						}
						bars = append(bars, curBar)
						instBars[InstrumentID] = bars
					} else { // 更新现有 bar
						curBar.High = math.Max(curBar.High, last)
						curBar.Low = math.Min(curBar.Low, last)
						curBar.Close = last
						curBar.OpenInterest = oi
						curBar.Volume = volume // 保存 volume 写文件时再处理
					}

					// 记录分钟的tick数量
					if n, ok := minTicks[InstrumentID+curBar.DateTime]; !ok {
						minTicks[InstrumentID+curBar.DateTime] = 1
					} else {
						minTicks[InstrumentID+curBar.DateTime] = n + 1
					}
				}
			}
		}

		// 分钟数据写入
		var db *sql.DB
		if db, err = sql.Open("postgres", pgMin); err != nil {
			return "数据库打开错误", err
		}
		// 退出时关闭
		defer db.Close()
		res, _ := db.Exec(`delete from future.future_min where "TradingDay" = $1`, tradingDay)
		if cnt, err := res.RowsAffected(); err != nil {
			return "删除当日文件时报错", err
		} else if cnt > 0 {
			logrus.Info("delete data of ", tradingDay, " rows:", strconv.FormatInt(cnt, 10))
		}

		sqlStr := `INSERT INTO future.future_min ("DateTime", "Instrument", "Open", "High", "Low", "Close", "Volume", "OpenInterest", "TradingDay") VALUES('%s', '%s', %.4f, %.4f, %.4f, %.4f, %d, %.4f, '%s');`
		// 开启入库事务
		tx, _ := db.Begin()
		for inst, bars := range instBars {
			// 按datetime排序
			sort.Sort(bars)
			preVol := int32(0)
			for _, bar := range bars {
				// 处理开收盘时间数据
				if n, ok := minTicks[inst+bar.DateTime]; !ok || n <= 1 {
					continue
				}
				t, err := time.Parse("2006010215:04:05", bar.DateTime)
				if err != nil {
					return "时间格式错误!", err
				}
				vol := bar.Volume - preVol
				if vol == 0 {
					continue
				}
				s := fmt.Sprintf(sqlStr, t.Format("2006-01-02 15:04:05"), inst, bar.Open, bar.High, bar.Low, bar.Close, vol, bar.OpenInterest, tradingDay)
				tx.Exec(s)
				preVol = bar.Volume
			}
		}
		err = tx.Commit()
		if err != nil {
			tx.Rollback()
			return "入库错误", err
		}
		logrus.Info(tradingDay, " finished.")
		return "", nil
	}
	return "读取xml", err
}
