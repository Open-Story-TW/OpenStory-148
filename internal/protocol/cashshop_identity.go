package protocol

import "time"

func addCashIdentity(w *Writer, cashID, expiresAt int64) {
	if cashID > 0 {
		w.U8(1)
		w.I64(cashID)
	} else {
		w.U8(0)
	}
	if expiresAt > 0 {
		w.I64(mapleTime(time.UnixMilli(expiresAt)))
	} else {
		w.I64(maxTime)
	}
}
