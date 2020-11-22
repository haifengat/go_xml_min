package main

import (
	"flag"
	"fmt"
	"xmlmin/src"
)

var (
	singleDay = ""
	startDay  = ""
)

func init() {
	flag.StringVar(&singleDay, "s", "", "处理指定某一天的数据")
	flag.StringVar(&startDay, "m", "", "从指定日期开始处理数据, 默认为空, 取库中最大交易日的下一日处理")
}

func main() {
	flag.Parse()
	if singleDay != "" {
		if msg, err := src.RunOnce(singleDay); err != nil {
			fmt.Printf("%s: %v\n", msg, err)
		}
	} else {
		src.Run(startDay)
	}
}
