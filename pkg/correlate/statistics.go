package correlate

import "time"

type Statistics struct {
	start int64
	end   int64
	laps  []int64

	sum    int64
	expAvg int64
	count  int64
}

func NewStopWatch() *Statistics {
	return &Statistics{}
}

func (sw *Statistics) Start() *Statistics {
	sw.start = time.Now().UnixNano()
	return sw
}

func (sw *Statistics) Stop() *Statistics {
	sw.end = time.Now().UnixNano()
	return sw
}

func (sw *Statistics) Elapsed() int64 {
	return sw.end - sw.start
}

func (sw *Statistics) Reset() {
	sw.start = 0
	sw.end = 0
}

func (sw *Statistics) Lap() {
	elapsed := sw.Elapsed()
	sw.laps = append(sw.laps, elapsed)
	sw.sum += elapsed
	sw.count++
}

func (sw *Statistics) LapWith(elapsed int64) {
	sw.laps = append(sw.laps, elapsed)
	sw.sum += elapsed
	sw.count++
}

//
//func (sw *Statistics) ExpAvg() int64 {
//	return sw.expAvg
//}

func (sw *Statistics) Avg() int64 {
	return sw.sum / sw.count
}

func (sw *Statistics) Sum() int64 {
	return sw.sum
}

func (sw *Statistics) Count() int64 {
	return sw.count
}

func (sw *Statistics) AvgDuration() time.Time {
	return time.Unix(0, sw.Avg())
}
