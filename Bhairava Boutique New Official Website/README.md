# Bhairava Boutique — Go backend, $0 public setup

This package keeps **Go as the backend** and PostgreSQL as the database.

## What is free

- Go backend: free/open source
- PostgreSQL: free when run on your own computer/server
- HTTPS/public access: Cloudflare Tunnel can be used at no charge
- WhatsApp: `wa.me` pre-filled message; user presses Send
- SMS: `sms:` pre-filled message; user presses Send
- Email button: `mailto:` pre-filled message
- Automatic email: optional Gmail SMTP using a Google App Password
- No Twilio, paid SMS API, or paid WhatsApp API

## Important $0 limitation

For a strict $0 setup with no hosting bill, the Go server and PostgreSQL must run on a computer/server you control. That computer must remain powered on and connected to the internet for the public site to work.

A Cloudflare Tunnel exposes the local Go server over HTTPS without opening inbound router ports. This is a free tunnel option, but Cloudflare can change its free-plan terms in the future. This setup does not require a paid domain.

## 1. PostgreSQL

Create the database:

```bash
psql -U postgres -h 127.0.0.1 -d postgres
```

Then run:

```sql
CREATE DATABASE bhairava_boutique;
```

Apply the schema:

```bash
psql -U postgres -h 127.0.0.1 -d bhairava_boutique -f database/schema.sql
```

## 2. Configure `.env`

Copy `.env.example` to `.env` and fill it in. Example:

```env
DATABASE_URL=postgres://postgres:YOUR_PASSWORD@127.0.0.1:5432/bhairava_boutique?sslmode=disable
JWT_SECRET=use-a-long-random-secret
ADMIN_EMAIL=your-admin-email
ADMIN_PASSWORD=use-a-strong-password
PORT=8080

SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USER=yourgmail@gmail.com
SMTP_PASSWORD=your-google-app-password
MAIL_FROM=yourgmail@gmail.com
NOTIFY_EMAIL=your-notification-email@gmail.com

WHATSAPP_NUMBER=919876543210
```

Never commit `.env` to Git.

## 3. Run the Go backend

Install Go 1.25+.

```bash
go mod download
go run .
```

Open locally:

`http://127.0.0.1:8080`

## 4. Expose it publicly for $0

Install `cloudflared` from Cloudflare's official download page, then run:

```bash
cloudflared tunnel --url http://127.0.0.1:8080
```

Cloudflared will print an HTTPS `trycloudflare.com` URL. Anyone with that URL can open the website and book appointments while your Go server is running.

This quick tunnel is intended for development/testing. For a persistent public address, create a named Cloudflare Tunnel and connect it to your own domain. A custom domain may cost money, so the strict $0 version uses the generated `trycloudflare.com` address.

## 5. Free WhatsApp/SMS/email behavior

After a successful appointment, the confirmation popup provides:

- Send WhatsApp — opens WhatsApp with the message pre-filled
- Send SMS — opens the phone's SMS app with the message pre-filled
- Send Email — opens the email client with the message pre-filled
- Close & Go Home — closes the popup and redirects home

These three message buttons do not send silently and do not incur API charges.

## Production safety notes

- Use a strong `JWT_SECRET`.
- Use a strong admin password.
- Keep `.env` private.
- Keep PostgreSQL bound to localhost unless you explicitly need remote access.
- Back up the PostgreSQL database regularly.
- Keep the host computer patched and protected.
- For true 24/7 public production, a paid always-on server is more reliable; the $0 setup depends on your own computer staying online.
