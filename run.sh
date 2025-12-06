#!/bin/bash

# Proxy Settings (Required for Binance/AI in some regions)
export HTTP_PROXY=http://127.0.0.1:54935
export HTTPS_PROXY=http://127.0.0.1:54935

# Binance API Keys (Real Trading)
export BINANCE_API_KEY="your_binance_api_key_here"
export BINANCE_SECRET_KEY="your_binance_secret_key_here"

# AI API Keys (e.g. DeepSeek, OpenAI)
export AI_API_KEY="your_ai_api_key_here"
export AI_API_URL="https://api.deepseek.com/v1/chat/completions"
export AI_MODEL="deepseek-chat"

# Run the bot
go run .
