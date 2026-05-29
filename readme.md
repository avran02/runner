# Lightweight GitLab Deploy Runner

Легковесный webhook-runner для автодеплоя из GitLab. Без тяжёлых CI/CD систем.

**📖 Для настройки приватных репозиториев и тестирования см. [setup.md](setup.md)**

## Что исправлено

### 1. ✅ Working Directory
Теперь каждая команда выполняется в указанной директории:
```yaml
projects:
  "my-secret-token":
    working_dir: "/var/www/backend"  # <- все команды выполнятся здесь
    deploy_steps: [...]
```

### 2. ✅ Множество проектов с индивидуальными секретами
Каждый проект имеет свой уникальный секрет (ключ в map):
```yaml
projects:
  "backend-secret-xyz":
    name: "Backend API"
    working_dir: "/var/www/backend"
    deploy_steps: [...]
  
  "frontend-secret-abc":
    name: "Frontend"
    working_dir: "/var/www/frontend"
    deploy_steps: [...]
```

**Преимущества:**
- Каждый проект = уникальный секрет
- Можно отозвать доступ одного проекта, не трогая остальные
- Красивые названия в Telegram уведомлениях

### 3. ✅ Debounce для множественных коммитов
Когда вы мержите dev → master с 10 коммитами, GitLab отправит 10 webhook'ов.

**Решение:** debounce timer склеивает их в один деплой:
```yaml
projects:
  "my-secret":
    debounce_time: "30s"  # подождёт 30 сек, задеплоит только последний SHA
```

**Как работает:**
- Приходит webhook #1 (SHA: abc123) → запускается таймер 30s
- Через 5 сек приходит webhook #2 (SHA: def456) → таймер сбрасывается, новый таймер 30s
- Через 10 сек приходит webhook #3 (SHA: xyz789) → таймер сбрасывается, новый таймер 30s
- Проходит 30 сек без новых webhook'ов → деплоится только последний SHA (xyz789)

## Настройка GitLab

**Для каждого проекта:**

1. Settings → Webhooks
2. URL: `http://your-server:8080/deploy`
3. Secret token: уникальный секрет для этого проекта (например `backend-secret-xyz`)
4. Trigger: **Push events** → только `master` branch
5. ❗ **Важно:** секрет из GitLab должен совпадать с ключом в `config.yml`

## Пример конфига

```yaml
server:
  port: "8080"

deploy_header_key: "X-Gitlab-Token"

projects:
  # Ключ = секрет из GitLab webhook
  "backend-secret-xyz":
    name: "Backend API"
    working_dir: "/var/www/backend"
    debounce_time: "30s"
    deploy_steps:
      - cmd: "docker"
        args: ["compose", "pull"]
      - cmd: "docker"
        args: ["compose", "up", "-d"]

  "frontend-secret-abc":
    name: "Frontend"
    working_dir: "/var/www/frontend"
    debounce_time: "15s"
    deploy_steps:
      - cmd: "npm"
        args: ["run", "build"]

telegram:
  enabled: true
  bot_token: "YOUR_BOT_TOKEN"
  chat_id: 123456789
  thread_id: 0
```

## Telegram уведомления

Теперь в Telegram будут красивые названия:

```
🚀 Deploy started
Project: Backend API
SHA: abc123def

✅ Deploy successful
Project: Backend API
SHA: abc123def
Duration: 45s
```

## Запуск

```bash
# Скомпилировать
go build -o deploy-runner .

# Запустить
./deploy-runner
```

## Логи

Все команды логируются с output'ом:
```
CMD [/var/www/backend]: docker [compose pull]
<docker output>
```

## Безопасность

- ✅ Constant-time secret comparison (защита от timing attacks)
- ✅ Таймауты на команды (30 минут max)
- ✅ Mutex на деплой (не будет параллельных запусков)
- ✅ HTTP read/write timeouts

## Зачем не GitLab CI?

Для прототипов и маленьких серверов GitLab Runner слишком тяжёлый:
- Жрёт ~500MB RAM в idle
- Нужны Docker-in-Docker конфигурации
- Сложная настройка для простых задач

Этот runner:
- ~10MB RAM
- Один бинарник
- Конфиг на 20 строк