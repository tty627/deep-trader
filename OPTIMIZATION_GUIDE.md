# 后端代码优化建议

## 已实现的优化

### 1. 添加辅助函数减少重复代码

我已经在 `binance_exchange.go` 中添加了两个辅助函数：

#### `formatQuantity(symbol string, quantity float64) string`
- 统一处理不同币种的下单数量精度格式化
- 消除了代码中多处重复的 if-else 精度判断逻辑

#### `mapOrderSide(actionType string, positionSide string) (futures.SideType, futures.PositionSideType)`
- 统一处理开仓/平仓时的订单方向映射
- `actionType`: "open" (开仓) 或 "close" (平仓/止盈止损)
- `positionSide`: "LONG" 或 "SHORT"

## 建议应用的优化点

### 1. 重构 `SetStopLoss` 函数

**当前代码** (约 line 898-927):
```go
func (e *BinanceExchange) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
    var side futures.SideType
    var posSide futures.PositionSideType

    if positionSide == "LONG" {
        side = futures.SideTypeSell
        posSide = futures.PositionSideTypeLong
    } else {
        side = futures.SideTypeBuy
        posSide = futures.PositionSideTypeShort
    }

    // 简单格式化 quantity (应与开仓保持一致)
    qtyStr := fmt.Sprintf("%.3f", quantity)
    if strings.Contains(symbol, "SOL") { qtyStr = fmt.Sprintf("%.1f", quantity) }
    if strings.Contains(symbol, "DOGE") { qtyStr = fmt.Sprintf("%.0f", quantity) }

    _, err := e.Client.NewCreateOrderService().
        Symbol(symbol).
        Side(side).
        PositionSide(posSide).
        Type(futures.OrderTypeStopMarket).
        StopPrice(fmt.Sprintf("%.4f", stopPrice)).
        Quantity(qtyStr).
        WorkingType(futures.WorkingTypeContractPrice).
        ClosePosition(true).
        Do(context.Background())

    return err
}
```

**优化后代码**:
```go
func (e *BinanceExchange) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
    // 1. 映射方向
    side, posSide := e.mapOrderSide("close", positionSide)
    
    // 2. 格式化数量
    qtyStr := e.formatQuantity(symbol, quantity)

    // 3. 发送订单
    _, err := e.Client.NewCreateOrderService().
        Symbol(symbol).
        Side(side).
        PositionSide(posSide).
        Type(futures.OrderTypeStopMarket).
        StopPrice(fmt.Sprintf("%.4f", stopPrice)).
        Quantity(qtyStr).
        WorkingType(futures.WorkingTypeContractPrice).
        ClosePosition(true).
        Do(context.Background())

    return err
}
```

### 2. 重构 `SetTakeProfit` 函数

**如果函数存在**，同样使用 `mapOrderSide` 和 `formatQuantity` 辅助函数进行简化。

### 3. 简化 `ExecuteDecision` 中的精度处理

**当前代码** (约 line 571-584):
```go
// 基础精度处理（实际生产环境应根据 ExchangeInfo 动态获取）
qtyStr := fmt.Sprintf("%.3f", quantity)
if strings.Contains(symbol, "BTC") {
    qtyStr = fmt.Sprintf("%.3f", quantity)
}
if strings.Contains(symbol, "ETH") {
    qtyStr = fmt.Sprintf("%.3f", quantity)
}
if strings.Contains(symbol, "SOL") {
    qtyStr = fmt.Sprintf("%.1f", quantity)
}
if strings.Contains(symbol, "DOGE") {
    qtyStr = fmt.Sprintf("%.0f", quantity)
}
```

**优化后代码**:
```go
qtyStr := e.formatQuantity(symbol, quantity)
```

同样，在 line 600-613 的平仓精度处理中也可以使用：
```go
qtyStr = e.formatQuantity(symbol, p.Quantity)
```

### 4. 简化 `handlePartialClose` 中的精度处理

**当前代码** (约 line 800-802):
```go
// 格式化精度 (简单处理)
qtyStr := fmt.Sprintf("%.3f", closeQty)
if strings.Contains(symbol, "SOL") { qtyStr = fmt.Sprintf("%.1f", closeQty) }
if strings.Contains(symbol, "DOGE") { qtyStr = fmt.Sprintf("%.0f", closeQty) }
```

**优化后代码**:
```go
qtyStr := e.formatQuantity(symbol, closeQty)
```

## 其他优化建议

### 1. 错误处理改进

当前代码中很多地方忽略了错误（使用 `_`），在关键路径可以考虑更完善的错误处理和日志记录：

```go
// 当前
amt, _ := strconv.ParseFloat(p.PositionAmt, 64)

// 建议
amt, err := strconv.ParseFloat(p.PositionAmt, 64)
if err != nil {
    log.Printf("⚠️ Failed to parse PositionAmt for %s: %v", p.Symbol, err)
    continue
}
```

### 2. 常量提取

建议将魔法数字提取为常量：

```go
const (
    DefaultQuantityPrecision = 3
    SOLQuantityPrecision     = 1
    DOGEQuantityPrecision    = 0
    DefaultPricePrecision    = 4
)
```

### 3. Context 超时设置

当前所有 Binance API 调用都使用 `context.Background()`，建议使用带超时的 context：

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
_, err := e.Client.NewGetAccountService().Do(ctx)
```

## 应用优化的步骤

1. **备份当前代码**:
   ```bash
   cp binance_exchange.go binance_exchange.go.backup
   ```

2. **逐个应用优化**，每次修改后编译测试：
   ```bash
   go build
   ```

3. **运行测试** (如果有):
   ```bash
   go test ./...
   ```

## 性能影响

这些优化主要是提升代码**可维护性**和**可读性**，对运行时性能影响微乎其微：
- 函数调用开销：纳秒级，可以忽略
- 代码行数减少：约 50-100 行
- 未来维护成本：显著降低

## 总结

当前代码已经是**功能完整且运行良好**的。上述优化属于"Nice to have"级别，主要目的是：
1. 减少重复代码（DRY 原则）
2. 提升代码可读性
3. 降低未来维护成本
4. 方便后续扩展（如添加新币种）

如果系统当前运行稳定，可以在不忙的时候逐步应用这些优化。
