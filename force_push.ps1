if (Test-Path .git/rebase-merge) { git rebase --abort }
if (Test-Path .git/rebase-apply) { git rebase --abort }
git add .
git commit -m "Force sync with local strategy"
git push -u origin main --force