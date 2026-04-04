package alerting

import log "github.com/sirupsen/logrus"

// LogHook forwards selected error logs to the global notifier.
type LogHook struct{}

// Levels implements logrus.Hook.
func (LogHook) Levels() []log.Level {
	return []log.Level{
		log.PanicLevel,
		log.FatalLevel,
		log.ErrorLevel,
	}
}

// Fire implements logrus.Hook.
func (LogHook) Fire(entry *log.Entry) error {
	globalNotifier.notifyErrorEntry(entry)
	return nil
}
