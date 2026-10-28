package desktop

import (
	"os"
	"sync"
)

const maxApplicationLog = 1024 * 1024

// Both supervisor and child output pass through this writer. Truncating an
// already protected file preserves its ACL and bounds long-running diagnostics.
type applicationLog struct {
	mu   sync.Mutex
	file *os.File
	size int
}

func (log *applicationLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	original := len(data)
	if len(data) > maxApplicationLog {
		data = data[len(data)-maxApplicationLog:]
	}
	if log.size+len(data) > maxApplicationLog {
		if err := log.file.Truncate(0); err != nil {
			return 0, err
		}
		if _, err := log.file.Seek(0, 0); err != nil {
			return 0, err
		}
		log.size = 0
	}
	n, err := log.file.Write(data)
	log.size += n
	if err != nil {
		return n, err
	}
	return original, nil
}
