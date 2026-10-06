package main

import (
	"fmt"
	"time"

	calendar "github.com/bjo/go-market-calendars"
)

func main() {
	nyse := calendar.XNYS()

	now := time.Now()
	fmt.Println("business day:", nyse.IsBusinessDay(now))
	fmt.Println("holiday:", nyse.IsHoliday(now))
	fmt.Println("early close:", nyse.IsEarlyClose(now))
	fmt.Println("open now:", nyse.IsOpen(now))

	first, last := nyse.Range()
	fmt.Printf("coverage: %s to %s\n", first.Format(time.DateOnly), last.Format(time.DateOnly))

	for _, h := range nyse.Holidays(time.Date(2025, 1, 1, 0, 0, 0, 0, calendar.NewYork), time.Date(2025, 12, 31, 0, 0, 0, 0, calendar.NewYork)) {
		fmt.Println(h.Date.Format("2006-01-02 Mon"), h.Name)
	}
}
