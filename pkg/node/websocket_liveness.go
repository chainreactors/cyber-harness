package node

import "time"

type websocketLiveness struct {
	pongWait   time.Duration
	pingPeriod time.Duration
	writeWait  time.Duration
}

func defaultWebsocketLiveness() websocketLiveness {
	return websocketLiveness{
		pongWait:   90 * time.Second,
		pingPeriod: 30 * time.Second,
		writeWait:  10 * time.Second,
	}
}

func (l websocketLiveness) normalized() websocketLiveness {
	defaults := defaultWebsocketLiveness()
	if l.pongWait <= 0 {
		l.pongWait = defaults.pongWait
	}
	if l.pingPeriod <= 0 {
		l.pingPeriod = defaults.pingPeriod
	}
	if l.writeWait <= 0 {
		l.writeWait = defaults.writeWait
	}
	return l
}
