package app

import "time"

// DeployInfo carries all data about a deploy event passed to notifiers.
type DeployInfo struct {
	ProjectName    string
	SHA            string
	Branch         string
	Author         string
	CommitURL      string
	ProjectURL     string
	Duration       time.Duration
	Err            error
	CommitsSummary string
	Steps          []DeployStep
}

// DeployHandle allows updating a notification as the deploy progresses.
// Returned by Notifier.Start; nil return means the notifier is unavailable.
type DeployHandle interface {
	Progress(stepIdx int, status stepStatus)
	Success(info DeployInfo)
	Fail(info DeployInfo)
}

// Notifier sends deploy notifications.
type Notifier interface {
	Start(info DeployInfo) DeployHandle
}

// buildNotifier assembles the active notifier from config and the available backends.
// Backends with a nil value are silently skipped.
func buildNotifier(cfg NotifyConfig, backends map[string]Notifier) Notifier {
	channels := cfg.Channels
	if len(channels) == 0 {
		channels = []string{"telegram", "ntfy"}
	}

	active := make([]Notifier, 0, len(channels))
	for _, ch := range channels {
		if n, ok := backends[ch]; ok && n != nil {
			active = append(active, n)
		}
	}

	return newChainNotifier(active, cfg.Mode == "fallback")
}

func newChainNotifier(notifiers []Notifier, fallback bool) Notifier {
	switch len(notifiers) {
	case 0:
		return nopNotifier{}
	case 1:
		return notifiers[0]
	}
	return &chainNotifier{notifiers: notifiers, fallback: fallback}
}

type chainNotifier struct {
	notifiers []Notifier
	fallback  bool
}

func (c *chainNotifier) Start(info DeployInfo) DeployHandle {
	if c.fallback {
		for _, n := range c.notifiers {
			if h := n.Start(info); h != nil {
				return h
			}
		}
		return nopHandle{}
	}
	handles := make([]DeployHandle, 0, len(c.notifiers))
	for _, n := range c.notifiers {
		if h := n.Start(info); h != nil {
			handles = append(handles, h)
		}
	}
	if len(handles) == 0 {
		return nopHandle{}
	}
	return multiHandle(handles)
}

type multiHandle []DeployHandle

func (m multiHandle) Progress(idx int, status stepStatus) {
	for _, h := range m {
		h.Progress(idx, status)
	}
}
func (m multiHandle) Success(info DeployInfo) {
	for _, h := range m {
		h.Success(info)
	}
}
func (m multiHandle) Fail(info DeployInfo) {
	for _, h := range m {
		h.Fail(info)
	}
}

type nopNotifier struct{}

func (nopNotifier) Start(_ DeployInfo) DeployHandle { return nopHandle{} }

type nopHandle struct{}

func (nopHandle) Progress(_ int, _ stepStatus) {}
func (nopHandle) Success(_ DeployInfo)          {}
func (nopHandle) Fail(_ DeployInfo)             {}
