# Разработка и проверка

Нужны Go 1.26+ и Node.js/npm; зависимости закреплены в web/package-lock.json. CI проверяет Go 1.26/1.27. На Windows запускайте npm.cmd и команды Go отдельно либо используйте GNU Make через WSL.

```sh
make build
./bin/gateway init --data-dir .ssh/local-data --secrets-dir .ssh/local-secrets
./bin/gateway serve --dev-http --listen 127.0.0.1:8443 --data-dir .ssh/local-data --secrets-dir .ssh/local-secrets --xray-bin /absolute/path/to/xray
```

При init сохраните выведенный пароль администратора. Повторный init пароль не меняет. Откройте http://127.0.0.1:8443/ для локальной разработки. Без --dev-http сервер использует HTTPS; HTTP вне loopback запрещён. Production-каталоги и systemd описаны в installation.md.

```sh
make check
curl http://127.0.0.1:8443/api/v1/health
```

make check выполняет frontend lint/typecheck/test/build, проверку gofmt, vet, Go-тесты и Linux amd64 build. В чистом checkout web/dist отсутствует: frontend обязательно собирается до Go. Node.js не нужен на сервере после сборки встроенного UI. Остальные API кроме health и auth требуют входа; изменяющие запросы требуют X-CSRF-Token.

Локальные Go-тесты не меняют системную сеть. Реальные VLESS SOCKS5/HTTP проверки на Ubuntu описаны в test-environment.md. Full Tunnel, AWG и независимый rollback требуют отдельной интеграционной приёмки. После изменения UI пересоберите Go-бинарник и перезапустите его: embed содержит снимок assets на момент компиляции.