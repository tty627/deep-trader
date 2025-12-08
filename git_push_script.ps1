git init
git add .
git commit -m "Update strategy: 10x leverage, relaxed RR, log added"
git branch -M main
if (git remote get-url origin) {
    git remote set-url origin https://github.com/tty627/deep-trader.git
} else {
    git remote add origin https://github.com/tty627/deep-trader.git
}
try {
    git pull origin main --rebase
} catch {
    Write-Host "Pull failed or nothing to pull, continuing..."
}
git push -u origin main
