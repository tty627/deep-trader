#!/usr/bin/env python3
"""Simple script to download historical Kline data from Binance and export to CSV.

Usage example:

    python3 binance_export_klines.py --symbol BTCUSDT --interval 1h --days 30 \
        --output data/BTCUSDT_1h_last30d.csv

This script only uses the public Kline endpoint and does NOT require API keys.
"""

import argparse
import csv
import datetime as dt
import json
import time
from pathlib import Path
from typing import List, Any
from urllib.parse import urlencode
from urllib.request import urlopen
from urllib.error import HTTPError, URLError
import ssl


BASE_URL = "https://api.binance.com/api/v3/klines"

# WARNING: this context disables SSL certificate verification to avoid local
# certificate issues. For production use, you should configure proper CA
# certificates and use ssl.create_default_context() instead.
SSL_CONTEXT = ssl._create_unverified_context()
MAX_HTTP_RETRIES = 5


def fetch_klines(
    symbol: str,
    interval: str,
    start_ms: int,
    end_ms: int,
    limit: int = 1000,
    pause: float = 0.2,
) -> List[List[Any]]:
    """Fetch klines from Binance between start_ms and end_ms (both in ms).

    Automatically handles pagination until all data in the range is fetched.
    """
    all_klines: List[List[Any]] = []
    current_start = start_ms

    while True:
        params = {
            "symbol": symbol.upper(),
            "interval": interval,
            "startTime": current_start,
            "endTime": end_ms,
            "limit": limit,
        }
        url = f"{BASE_URL}?{urlencode(params)}"

        # Simple retry logic for transient HTTP errors (5xx)
        last_error: Exception | None = None
        for attempt in range(MAX_HTTP_RETRIES):
            try:
                with urlopen(url, context=SSL_CONTEXT) as resp:
                    data = json.loads(resp.read().decode("utf-8"))
                break
            except HTTPError as e:
                last_error = e
                if 500 <= e.code < 600 and attempt < MAX_HTTP_RETRIES - 1:
                    # Transient server-side error, retry with backoff
                    wait = pause * (attempt + 1)
                    print(f"HTTP {e.code} from Binance, retrying in {wait:.1f}s...")
                    time.sleep(wait)
                    continue
                raise SystemExit(f"HTTP error from Binance: {e.code} {e.reason}")
            except URLError as e:
                last_error = e
                if attempt < MAX_HTTP_RETRIES - 1:
                    wait = pause * (attempt + 1)
                    print(f"Network error {e.reason}, retrying in {wait:.1f}s...")
                    time.sleep(wait)
                    continue
                raise SystemExit(f"Network error: {e.reason}")

        if not isinstance(data, list):
            raise SystemExit(f"Unexpected response: {data}")

        if not data:
            break

        all_klines.extend(data)

        # If fewer than `limit` records, we've reached the end
        if len(data) < limit:
            break

        last_open_time = data[-1][0]

        # If we've reached or passed the end time, stop
        if last_open_time >= end_ms:
            break

        # Next page: start just after the last open time
        current_start = last_open_time + 1

        # Be kind to the API
        time.sleep(pause)

    return all_klines


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Download Binance historical Kline data and export to CSV.",
    )
    parser.add_argument(
        "--symbol",
        default="BTCUSDT",
        help="Trading pair symbol, e.g. BTCUSDT, ETHUSDT (default: BTCUSDT)",
    )
    parser.add_argument(
        "--interval",
        default="1h",
        help=(
            "Kline interval, e.g. 1m, 5m, 15m, 1h, 4h, 1d. "
            "Must be a valid Binance interval (default: 1h)."
        ),
    )
    parser.add_argument(
        "--days",
        type=int,
        default=30,
        help=(
            "How many days of history to fetch, counting backwards from now (UTC). "
            "Default: 30. Ignored if --start or --end is provided."
        ),
    )
    parser.add_argument(
        "--start",
        type=str,
        default=None,
        help=(
            "Start time (UTC) in ISO format, e.g. 2024-01-01 or 2024-01-01T00:00:00. "
            "If provided, used together with --end/--days to build the time range."
        ),
    )
    parser.add_argument(
        "--end",
        type=str,
        default=None,
        help=(
            "End time (UTC) in ISO format, e.g. 2024-03-01 or 2024-03-01T00:00:00. "
            "Default: now (UTC) if omitted."
        ),
    )
    parser.add_argument(
        "--output",
        type=str,
        default=None,
        help=(
            "Output CSV file path. Default: data/<SYMBOL>_<INTERVAL>_last<days>d.csv "
            "or data/<SYMBOL>_<INTERVAL>_<start>_<end>.csv when using --start/--end."
        ),
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()

    # Always work in UTC
    now_utc = dt.datetime.now(dt.timezone.utc)

    # Build time range
    if args.start or args.end:
        if args.start:
            try:
                start_dt = dt.datetime.fromisoformat(args.start)
            except ValueError:
                raise SystemExit(
                    f"Invalid --start value: {args.start!r}. Use YYYY-MM-DD or full ISO datetime."
                )
            if start_dt.tzinfo is None:
                start_dt = start_dt.replace(tzinfo=dt.timezone.utc)
            else:
                start_dt = start_dt.astimezone(dt.timezone.utc)
        else:
            # If only --end is provided, derive start from end - days
            if not args.end:
                raise SystemExit("At least one of --start or --days must be provided.")
            # end will be parsed below, for now assume now_utc then adjust after parsing
            start_dt = None  # type: ignore[assignment]

        if args.end:
            try:
                end_dt = dt.datetime.fromisoformat(args.end)
            except ValueError:
                raise SystemExit(
                    f"Invalid --end value: {args.end!r}. Use YYYY-MM-DD or full ISO datetime."
                )
            if end_dt.tzinfo is None:
                end_dt = end_dt.replace(tzinfo=dt.timezone.utc)
            else:
                end_dt = end_dt.astimezone(dt.timezone.utc)
        else:
            end_dt = now_utc

        if start_dt is None:
            # Only --end given: derive start from end - days
            start_dt = end_dt - dt.timedelta(days=args.days)
    else:
        end_dt = now_utc
        start_dt = end_dt - dt.timedelta(days=args.days)

    start_ms = int(start_dt.timestamp() * 1000)
    end_ms = int(end_dt.timestamp() * 1000)

    symbol = args.symbol.upper()
    interval = args.interval

    if args.output:
        out_path = Path(args.output)
    else:
        out_dir = Path("data")
        out_dir.mkdir(parents=True, exist_ok=True)
        if args.start or args.end:
            start_str = start_dt.strftime("%Y%m%d")
            end_str = end_dt.strftime("%Y%m%d")
            out_path = out_dir / f"{symbol}_{interval}_{start_str}_{end_str}.csv"
        else:
            out_path = out_dir / f"{symbol}_{interval}_last{args.days}d.csv"

    print(
        f"Fetching klines from Binance: symbol={symbol}, interval={interval}, "
        f"range={start_dt.isoformat()} to {end_dt.isoformat()} (UTC)"
    )

    klines = fetch_klines(symbol, interval, start_ms, end_ms)

    if not klines:
        print("No data returned. Please check symbol/interval and try a different range.")
        return

    print(f"Fetched {len(klines)} klines. Writing to {out_path} ...")

    out_path.parent.mkdir(parents=True, exist_ok=True)

    # Binance kline fields per entry:
    # [
    #   0  open time
    #   1  open
    #   2  high
    #   3  low
    #   4  close
    #   5  volume
    #   6  close time
    #   7  quote asset volume
    #   8  number of trades
    #   9  taker buy base asset volume
    #   10 taker buy quote asset volume
    #   11 ignore
    # ]

    header = [
        "open_time",
        "open",
        "high",
        "low",
        "close",
        "volume",
        "close_time",
        "quote_volume",
        "num_trades",
        "taker_buy_base_volume",
        "taker_buy_quote_volume",
        "ignore",
    ]

    with out_path.open("w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(header)
        writer.writerows(klines)

    print("Done.")
    print(f"Saved to: {out_path.resolve()}")


if __name__ == "__main__":
    main()
