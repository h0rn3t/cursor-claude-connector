# 🚀 Deployment Guide - Cursor Claude Connector

This guide will help you connect Cursor with your Claude subscription using this proxy.

## 📋 Prerequisites

1. **Active Claude subscription** (Pro or Max)
2. **Cursor IDE** installed on your local machine
3. **Go 1.26+** (for source builds) **or Docker** (for container deploys)
4. **Upstash Redis** account — free tier works

## 🚀 Deployment Options

### Option 1: Container image (Recommended) 🐳

Build and run anywhere — Fly, Render, Railway, a VPS, or your laptop:

```bash
docker build -t cursor-claude-connector .
docker run --rm -p 9095:9095 \
  -e UPSTASH_REDIS_REST_URL=... \
  -e UPSTASH_REDIS_REST_TOKEN=... \
  -e API_KEY=... \
  cursor-claude-connector
```

The image is built from `golang:1.26-alpine` and ships a `distroless/static-debian12` runtime, so the final image is small and contains only the static binary.

### Option 2: Manual server deployment

If you prefer to deploy on your own server with a binary:

#### 1. Install Go 1.26+

```bash
# macOS
brew install go
# Linux
wget https://go.dev/dl/go1.26.4.linux-amd64.tar.gz
tar -C /usr/local -xzf go1.26.4.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

#### 2. Clone and configure

```bash
git clone https://github.com/Maol-1997/cursor-claude-connector.git
cd cursor-claude-connector
cp env.example .env
# Edit .env with your Upstash credentials
```

#### 3. Set up Upstash Redis

1. Create a free account at [Upstash Console](https://console.upstash.com/)
2. Create a new Redis database
3. Copy the REST URL and REST Token to your `.env` file

#### 4. Build and start the server

```bash
./start.sh              # builds + runs in one step
# or:
make build              # produces ./cursor-claude-connector
PORT=3000 ./cursor-claude-connector
```

The `start.sh` script will:

- Verify Go 1.26+ is available
- Build the binary if it's missing
- Start the server on your specified port

## 🔐 Claude Authentication

### 1. Access the web interface

Open your browser and navigate to:

```
http://your-server-ip:9095/
```

Or if using a custom port:

```
http://your-server-ip:YOUR_PORT/
```

![Login Interface](images/login.webp)

### 2. Authentication process

1. Click **"Connect with Claude"**

   ![Claude OAuth Step 1](images/claude-oauth-1.webp)

2. A Claude window will open for authentication
3. Sign in with your Claude account (Pro/Max)
4. Authorize the application

   ![Claude OAuth Step 2](images/claude-oauth-2.webp)

5. You'll be redirected to a page with a code
6. Copy the ENTIRE code (it includes a # in the middle)
7. Paste it in the web interface field
8. Click **"Submit Code"**

### 3. Verify authentication

If everything went well, you'll see the message: **"You are successfully authenticated with Claude"**

![Logged In](images/logged-in.webp)

## 🖥️ Cursor Configuration

### 1. Open Cursor settings

1. In Cursor, press `Cmd+,` (Mac) or `Ctrl+,` (Windows/Linux)
2. Go to the **"Models"** section
3. Look for the **"Override OpenAI Base URL"** option

### 2. Configure the endpoint

1. Enable **"Override OpenAI Base URL"**
2. In the URL field, enter:

   **For Vercel deployment:**

   ```
   https://your-app-name.vercel.app/v1
   ```

   **For manual server deployment:**

   ```
   http://your-server-ip:9095/v1
   ```

   Examples:

   ```
   https://cursor-claude-proxy.vercel.app/v1
   http://54.123.45.67:9095/v1
   ```

![Cursor Custom URL Configuration](images/cursor-custom-url.webp)

### 3. Verify the connection

1. In the models list, you should see the available Claude models

2. Select your preferred model

3. Try typing something in Cursor's chat

## ✅ That's it!

You're now using Claude's full power directly in Cursor IDE. The proxy will handle all the communication between Cursor and Claude using your subscription.

## 🔍 Quick Troubleshooting

- **Can't connect?** Make sure the server is running (check the terminal where you ran `./start.sh`)
- **Authentication failed?** Try visiting `http://your-server-ip:PORT/auth/logout` and authenticate again
- **Models not showing?** Restart Cursor and make sure the URL ends with `/v1`
- **Using custom port?** Make sure to use the same port in both the server and Cursor configuration

---

Enjoy coding with Claude in Cursor! 🎉
