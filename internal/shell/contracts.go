package shell

import (
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func CapacityRows() []proxy.CapacityRow {
	return []proxy.CapacityRow{
		{Thing: "Engine mailbox", Protects: "proxy memory between socket goroutines and the engine", Capacity: strconv.Itoa(mailboxCap) + " events",
			Key: "(constant)", Full: "Readers and writers wait to post (back pressure)", Deadline: "-", Gauge: "engine.shell_mailbox_depth"},
		{Thing: "Unprocessed reads per connection", Protects: "proxy memory: a pause takes effect before the next read", Capacity: "1 read of " + strconv.Itoa(proxy.MaxRead) + " bytes",
			Key: "(constant)", Full: "The reader waits until the engine has taken the read", Deadline: "-", Gauge: "-"},
		{Thing: "Write queue per connection", Protects: "proxy memory if the engine's own bound were broken", Capacity: strconv.Itoa(writeQueueCap) + " bytes, " + strconv.Itoa(maxQueueItems) + " items",
			Key: "(constant)", Full: "Connection closed", Deadline: "timeouts.client_write per write", Gauge: "engine.shell_write_queue_overflows"},
		{Thing: "Live sockets", Protects: "file descriptors", Capacity: strconv.Itoa(maxLiveConns),
			Key: "(constant)", Full: "New sockets closed at once", Deadline: "-", Gauge: "engine.shell_accept_refused"},
	}
}
