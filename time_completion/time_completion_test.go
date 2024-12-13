package time_completion

import (
	"fmt"
	"testing"
	"time"
)

type TimerTrackerTest struct {
	keyName string
	repeat  int
}

func InitialDuration() time.Duration {
	initial := NewTimerTracker()

	initial.Track("pretest", func() {
		time.Sleep(time.Nanosecond)
	})

	fmt.Printf("Initial time offset: %d\n", initial.times["pretest"].durations[0])

	return initial.times["pretest"].durations[0]
}

func TestTimerTracker(t *testing.T) {
	initialDuration := InitialDuration()

	tests := []TimerTrackerTest{
		{"Test 1", 1},
		{"Test 5", 5},
	}

	for _, test := range tests {
		tracker := NewTimerTracker()

		for i := 0; i < test.repeat; i++ {
			tracker.Track(test.keyName, func() {
				time.Sleep(time.Nanosecond)
			})
		}

		if len(tracker.times[test.keyName].durations) != test.repeat {
			t.Fatalf("Expected %d function calls, got %d", test.repeat, len(tracker.times[test.keyName].durations))
		}

		duration := tracker.times[test.keyName].durations[0]

		if duration < time.Nanosecond || duration > initialDuration {
			t.Errorf("Expected duration of: \n%d <-range-> %d\n%d <= got", time.Nanosecond, initialDuration, duration)
		}
	}
}

func TestTimeFunction(t *testing.T) {

	duration := TimeFunction(func() {
		time.Sleep(time.Nanosecond)
	})

	if duration != time.Nanosecond {
		t.Errorf("Expected duration of: \n%d\n%d <= got", time.Nanosecond, duration)
	}
}
