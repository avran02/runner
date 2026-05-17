# Настройка для приватной репы и тестирование

## 1. Настройка SSH для приватного GitLab

### Создание deploy ключа

```bash
# На сервере, где будет запущен runner
ssh-keygen -t ed25519 -C "gitlab-deploy" -f ~/.ssh/id_rsa_gitlab -N ""

# Скопировать публичный ключ
cat ~/.ssh/id_rsa_gitlab.pub
```

### Добавление в GitLab

1. Перейти в проект → **Settings → Repository → Deploy Keys**
2. Вставить публичный ключ
3. ✅ Включить **"Grant write permissions"** (если нужно пушить)
4. Сохранить

### Проверка SSH

```bash
# Убедиться, что SSH работает
ssh -i ~/.ssh/id_rsa_gitlab -T git@your-gitlab.com

# Должно вернуть: "Welcome to GitLab, @username!"
```

### Первичный clone репозитория

```bash
# В рабочей директории сделать первый clone
cd /var/www/backend-api
GIT_SSH_COMMAND="ssh -i ~/.ssh/id_rsa_gitlab" git clone git@your-gitlab.com:company/backend.git .

# Проверить, что можно fetch'ить
GIT_SSH_COMMAND="ssh -i ~/.ssh/id_rsa_gitlab" git fetch origin
```

---

## 2. Тестирование без коммитов в GitLab

### Вариант 1: Локальный mock-сервер (рекомендую)

Создай файл `test_webhook.sh`:

```bash
#!/bin/bash

# Параметры
SECRET="backend-secret-token-xyz"
SHA="abc123def456"  # любой существующий SHA из твоей репы

# Отправка webhook'а
curl -X POST http://localhost:8080/deploy \
  -H "Content-Type: application/json" \
  -H "X-Gitlab-Token: $SECRET" \
  -d "{
    \"ref\": \"refs/heads/master\",
    \"checkout_sha\": \"$SHA\",
    \"after\": \"$SHA\"
  }"

echo ""
echo "Webhook отправлен для SHA: $SHA"
```

Использование:
```bash
chmod +x test_webhook.sh
./test_webhook.sh
```

### Вариант 2: Тестовый endpoint

Добавь в `main.go` тестовый handler (только для dev):

```go
// В main() после mux.HandleFunc("/deploy", ...)
if cfg.Server.Port == "8080" { // только на dev-порту
    mux.HandleFunc("/test-deploy", func(w http.ResponseWriter, r *http.Request) {
        secret := r.URL.Query().Get("secret")
        sha := r.URL.Query().Get("sha")
        
        if secret == "" || sha == "" {
            http.Error(w, "usage: /test-deploy?secret=xxx&sha=yyy", 400)
            return
        }
        
        // Симулируем webhook
        req, _ := http.NewRequest("POST", "http://localhost:8080/deploy", 
            strings.NewReader(fmt.Sprintf(`{"ref":"refs/heads/master","after":"%s"}`, sha)))
        req.Header.Set("X-Gitlab-Token", secret)
        req.Header.Set("Content-Type", "application/json")
        
        application.DeployHandler(w, req)
    })
}
```

Тогда можно просто:
```bash
curl "http://localhost:8080/test-deploy?secret=backend-secret-token-xyz&sha=abc123"
```

### Вариант 3: GitLab Test в UI

1. GitLab → Settings → Webhooks → твой webhook
2. Внизу в разделе **"Recent Deliveries"** найди любой старый запрос
3. Нажми **"Resend"** → это отправит тот же payload снова
4. Не создаёт новых коммитов!

---

## 3. Дебаг режим

Добавь в конфиг:

```yaml
server:
  port: "8080"
  debug: true  # логировать все входящие webhook'и
```

Обновлённый handler:

```go
func (a *App) DeployHandler(w http.ResponseWriter, r *http.Request) {
    if a.Cfg.Server.Debug {
        body, _ := io.ReadAll(r.Body)
        log.Printf("Incoming webhook:\nHeaders: %v\nBody: %s\n", r.Header, string(body))
        r.Body = io.NopCloser(bytes.NewBuffer(body)) // восстановить body
    }
    
    // остальной код...
}
```

---

## 4. Проверка деплоя вручную

```bash
# Запустить runner
./deploy-runner

# В другом терминале отправить тестовый webhook
curl -X POST http://localhost:8080/deploy \
  -H "X-Gitlab-Token: backend-secret-token-xyz" \
  -H "Content-Type: application/json" \
  -d '{
    "ref": "refs/heads/master",
    "after": "HEAD"
  }'

# Проверить логи runner'а
# Должен увидеть:
# - "Scheduled deploy for..."
# - "Executing deploy for..."
# - "CMD [/var/www/backend]: git fetch origin HEAD"
# - "✅ Deploy successful" в Telegram
```

---

## 5. Альтернатива SSH: HTTP с токеном

Если не хочешь возиться с SSH ключами:

```yaml
projects:
  "backend-secret":
    name: "Backend"
    working_dir: "/var/www/backend"
    git_token: "glpat-xxxxxxxxxxxx"  # GitLab Personal Access Token
```

В коде добавь:
```go
if cfg.GitToken != "" {
    // Настроить git credential helper
    runCmd(workingDir, "", "git", "config", "credential.helper", 
        fmt.Sprintf("!f() { echo \"username=oauth2\"; echo \"password=%s\"; }; f", cfg.GitToken))
}
```

Но SSH безопаснее и проще для серверов.

---

## 6. Быстрый smoke test

```bash
# 1. Скомпилировать
go build -o deploy-runner .

# 2. Создать минимальный config.yml
cat > config.yml << EOF
server:
  port: "8080"
deploy_header_key: "X-Gitlab-Token"
projects:
  "test-secret":
    name: "Test Project"
    working_dir: "/tmp/test-repo"
    ssh_key_path: "$HOME/.ssh/id_rsa_gitlab"
    deploy_steps:
      - cmd: "echo"
        args: ["Deploy works!"]
telegram:
  enabled: false
EOF

# 3. Подготовить тестовую репу
mkdir -p /tmp/test-repo
cd /tmp/test-repo
git init
git remote add origin git@your-gitlab.com:yourproject.git
GIT_SSH_COMMAND="ssh -i ~/.ssh/id_rsa_gitlab" git fetch origin

# 4. Запустить runner
./deploy-runner &
RUNNER_PID=$!

# 5. Отправить тестовый webhook
sleep 2
curl -X POST http://localhost:12341/deploy \
  -H "X-Gitlab-Token: movrqenoubhagjsdno" \
  -H "Content-Type: application/json" \
  -d '{"ref":"refs/heads/master","after":"HEAD", "checkout_sha": "2db1f755ee247e6b70bbba9f8a89af110e115109"}'

# 6. Проверить логи
sleep 3
cat deploy-runner.log  # если логируешь в файл

# 7. Убить runner
kill $RUNNER_PID
```

Если увидел `"Deploy works!"` в логах — всё работает!