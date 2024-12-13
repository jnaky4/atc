package time_completion

import (
	"filesystem/cli/colors"
	tc "filesystem/const/terminalColors"
	"fmt"
	"github.com/rs/zerolog"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"time"
)

//func main(){
//	defer Timer()()
//	test(20)
//	PrintTimerStatistics()
//}
//
//func test(i int){
//	defer FunctionTimerCounter(test)()
//	for j := 0; j < i; j++{
//		println(i * j)
//	}
//}

// FunctionStats represents the statistics for a function.

type FunctionStats struct {
	TotalTime time.Duration
	Count     int
}

var (
	statsMap   = make(map[string]FunctionStats)
	statsMutex sync.Mutex
)

// FunctionTimerCounter measures the time taken for a function to complete and stores the statistics.
func FunctionTimerCounter(i interface{}) func() {
	funcName := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
	start := time.Now()

	return func() {
		elapsed := time.Since(start)

		statsMutex.Lock()
		defer statsMutex.Unlock()

		stats, exists := statsMap[funcName]
		if !exists {
			stats = FunctionStats{}
		}

		stats.TotalTime += elapsed
		stats.Count++

		statsMap[funcName] = stats

		fmt.Printf("%s function elapsed: %s\n", funcName, elapsed)
	}
}

// PrintTimerStatistics prints the measurements of each function that called FunctionTimerCounter
func PrintTimerStatistics() {
	statsMutex.Lock()
	defer statsMutex.Unlock()

	for funcName, stats := range statsMap {
		averageTime := stats.TotalTime / time.Duration(stats.Count)
		fmt.Printf("%s - Total Time: %s, Average Time: %s\n", funcName, stats.TotalTime, averageTime)
		fmt.Printf("%s - Times Function Executed: %d", funcName, stats.Count)
	}

}

func Timer() func() {
	start := time.Now()
	return func() {
		fmt.Printf("elapsed: %s\n", time.Since(start))
		//fmt.Printf(colors.SetColor(fmt.Sprintf("%s\n", time.Since(start)), tc.Salmon))
	}
}

func TimeFunction(task func()) time.Duration {
	start := time.Now()
	task()
	return time.Since(start)
}

func FunctionTimer(i interface{}) func() {
	funcName := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
	start := time.Now()
	return func() {
		fmt.Printf("%s function elapsed: %s\n", funcName, time.Since(start))
	}
}

func LogTimer(log *zerolog.Logger, funcInfo string) func() {
	//funcName := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Id()
	start := time.Now()
	return func() {
		//log.Info().Msgf("%s time elapes: %s", funcName, time.Since(start))
		log.Info().Msgf("%s time elapsed: %s", funcInfo, time.Since(start))
	}
}

// todo multithread the calls, to speed up benchmarking
func TrackFunction(fn func(), repeat int) {
	// Ensure times is positive
	if repeat <= 0 {
		return
	}

	var totalTime time.Duration

	for i := 0; i < repeat; i++ {
		start := time.Now()

		fn()

		elapsed := time.Since(start)
		fmt.Printf("Elapsed -> %s\n", colors.SetColor(fmt.Sprintf("%s", elapsed), tc.Salmon))
		totalTime += elapsed
	}

	averageTime := totalTime / time.Duration(repeat)
	fmt.Printf(" %dns/%d=%s\n", totalTime, time.Duration(repeat), averageTime)

	// Print average time
	fmt.Printf("Function ran %s times, average time: %s\n", colors.SetColor(fmt.Sprintf("%d", repeat), tc.Salmon), colors.SetColor(fmt.Sprintf("%v", averageTime), tc.Salmon))
}

type TimeEntry struct {
	durations []time.Duration // Store each individual duration
}

type TimerTracker struct {
	sync.Mutex
	times map[string]*TimeEntry
}

func NewTimerTracker() *TimerTracker {
	return &TimerTracker{
		times: make(map[string]*TimeEntry),
	}
}

func (tracker *TimerTracker) Track(key string, task func()) {
	start := time.Now()
	task()                         // Run the function being tracked
	timeSpent := time.Since(start) // Get the elapsed time for this run

	tracker.Lock()
	defer tracker.Unlock()

	// Ensure entry exists in the map
	entry, exists := tracker.times[key]
	if !exists {
		entry = &TimeEntry{}
		tracker.times[key] = entry
	}

	// Append the individual execution time to the slice
	entry.durations = append(entry.durations, timeSpent)
}

func (tracker *TimerTracker) Report() {
	tracker.Lock()
	defer tracker.Unlock()

	for key, entry := range tracker.times {

		totalTime := time.Duration(0)
		for _, d := range entry.durations {
			//fmt.Printf("%s: %s\n", key, colors.SetColor(FormatDuration(d.Nanoseconds()), tc.Salmon))
			totalTime += d
		}

		averageTime := float64(totalTime) / float64(len(entry.durations))

		fmt.Printf("Average %s: Count: %d -> %s\n", colors.SetColor(FormatDuration(int64(averageTime)), tc.Salmon), len(entry.durations), colors.SetColor(key, tc.Mint))
	}
}

//func FormatDuration(duration int64) string {
//	switch {
//	case duration < 1000:
//		return strconv.FormatInt(duration, 10) + "ns"
//	case duration < 1000000:
//		return strconv.FormatInt(duration/1000, 10) + "us"
//	case duration < 1000000000:
//		return strconv.FormatInt(duration/1000000, 10) + "ms"
//	default:
//		return strconv.FormatInt(duration/1000000000, 10) + "s"
//	}
//}

func FormatDuration(duration int64) string {
	switch {
	case duration < 1000:
		return strconv.FormatInt(duration, 10) + "ns"
	case duration < 1000000:
		// Convert to microseconds and round to 2 decimal places
		us := float64(duration) / 1000
		return fmt.Sprintf("%.2fus", us)
	case duration < 1000000000:
		// Convert to milliseconds and round to 2 decimal places
		ms := float64(duration) / 1000000
		return fmt.Sprintf("%.2fms", ms)
	default:
		// Convert to seconds and round to 2 decimal places
		sec := float64(duration) / 1000000000
		return fmt.Sprintf("%.2fs", sec)
	}
}
