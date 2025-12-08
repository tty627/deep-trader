package main

import "errors"

// ErrPartialCloseSkipped 表示本次 partial_close 被风控/盈亏规则主动跳过，没有真正发送到交易所。
// 上层逻辑可以据此将 ExecStatus 标记为 "skipped"，避免误以为是下单失败。
var ErrPartialCloseSkipped = errors.New("partial close skipped by risk rule")
