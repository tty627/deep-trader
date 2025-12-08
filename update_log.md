# Update Log - 2025-12-08

## Strategy & Risk Management Updates

### 1. Leverage Adjustment (Lower Risk)
- **Objective**: Reduce the risk of stop-loss hunts and liquidation due to market noise, providing more "breathing room" for trades.
- **Change**:
  - Reduced fixed leverage from **20x** to **10x** in `risk.go` and `config.go`.
  - This applies to both BTC/ETH and Altcoins by default.

### 2. Risk/Reward (RR) Threshold Relaxation
- **Objective**: Allow the AI to take trades with "good enough" potential, rather than waiting for "perfect" setups that rarely occur in choppy markets.
- **Change**:
  - Lowered the hard constraint in `risk.go`:
    - Formal positions: Minimum RR reduced from **2.0** to **1.3**.
    - Probe positions (risk <= 1.5% equity): Minimum RR remains at **0.8**.
  - Updated AI System Prompt in `brain.go` to aim for **≥ 1.5:1** (down from 2:1) and accept **1.2:1** for strong trends.

### 3. AI Confidence Threshold Adjustment
- **Objective**: Encourage more activity when Sharpe Ratio is low (but not critical), avoiding excessive "paralysis by analysis."
- **Change**:
  - Updated System Prompt in `brain.go`:
    - When Sharpe Ratio is between -2.0 and 0: AI is now instructed to take trades with **Confidence > 60** (previously > 70/75).
    - Explicitly permits "moderate trial and error."

### 4. Market Bias Correction (Crowding & RSI)
- **Objective**: Fix the AI's tendency to be overly contrarian or fearful of strong trends due to "overbought" or "crowded" signals.
- **Change**:
  - Added a "Key Hints" section to the System Prompt in `brain.go`:
    - **Crowded Trades**: Explicitly states that `Bullish_Crowded` does NOT mean "do not buy." In strong trends, crowding is normal.
    - **RSI**: Warns against shorting solely based on RSI > 70, as indicators can remain overbought in strong trends.

### 5. Trailing Stop Flexibility
- **Objective**: Allow tighter trailing stops to protect profits without being rejected by the risk engine.
- **Change**:
  - In `risk.go`, reduced the minimum stop-loss distance buffer:
    - Absolute floor: Decreased from **0.25%** to **0.12%**.
    - ATR buffer factor: Decreased from **50%** to **20%**.
