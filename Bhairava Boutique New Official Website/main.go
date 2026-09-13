package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	// Ensures .env files are loaded

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

type App struct {
	db        *pgxpool.Pool
	templates *template.Template
	notify    *Notifier
	clients   map[chan []byte]struct{}
	clientsMu sync.Mutex
}

type Notifier struct {
	smtpHost     string
	smtpPort     int
	smtpUser     string
	smtpPassword string
	mailFrom     string
	notifyEmail  string
}

type Product struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	Price       float64   `json:"price"`
	Stock       int       `json:"stock"`
	ImageURL    string    `json:"image_url"`
	Active      bool      `json:"active"`
}
type Appointment struct {
	ID                      uuid.UUID `json:"id"`
	Name                    string    `json:"name"`
	Phone                   string    `json:"phone"`
	Email                   string    `json:"email"`
	AppointmentType         string    `json:"appointment_type"`
	AppointmentDate         string    `json:"appointment_date"`
	AppointmentTime         string    `json:"appointment_time"`
	Message                 string    `json:"message"`
	Status                  string    `json:"status"`
	NotificationDescription string    `json:"notification_description"`
}
type Claims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

func main() {

	// Load the .env file explicitly
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	// Now fetch the URL safely
	dbURL := os.Getenv("DATABASE_URL")
	log.Println("Connecting to:", dbURL)

	// dbURL := os.Getenv("DATABASE_URL")
	// if dbURL == "" {
	// 	dbURL = "postgres://postgres:postgres@localhost:5432/bhairava_boutique"
	// }
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err = pool.Ping(context.Background()); err != nil {
		log.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), `ALTER TABLE appointments ADD COLUMN IF NOT EXISTS notification_description TEXT NOT NULL DEFAULT ''`); err != nil {
		log.Fatal("could not prepare appointment notifications: ", err)
	}

	notifier := newNotifier()
	app := &App{db: pool, templates: template.Must(template.ParseGlob("templates/*.html")), notify: notifier, clients: make(map[chan []byte]struct{})}
	r := chi.NewRouter()
	r.Use(middleware.Logger, middleware.Recoverer)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))))
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/", app.home)
	r.Get("/products", app.productsPage)
	r.Get("/appointment", app.appointmentPage)
	r.Get("/admin/login", app.loginPage)
	r.Get("/admin", func(w http.ResponseWriter, r *http.Request) { app.templates.ExecuteTemplate(w, "admin.html", nil) })
	r.Get("/api/admin/events", app.adminEvents)
	r.Post("/api/login", app.login)
	r.Get("/api/products", app.listProducts)
	r.Get("/api/products/{id}", app.getProduct)
	r.Post("/api/appointments", app.createAppointment)
	r.Group(func(r chi.Router) {
		r.Use(app.auth)
		r.Post("/api/admin/products", app.createProduct)
		r.Get("/api/admin/products", app.listAdminProducts)
		r.Put("/api/admin/products/{id}", app.updateProduct)
		r.Delete("/api/admin/products/{id}", app.deleteProduct)
		r.Post("/api/admin/products/{id}/image", app.uploadProductImage)
		r.Get("/api/admin/appointments", app.listAppointments)
		r.Put("/api/admin/appointments/{id}/status", app.updateAppointmentStatus)
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Bhairava Boutique running on http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	a.templates.ExecuteTemplate(w, "index.html", nil)
}
func (a *App) productsPage(w http.ResponseWriter, r *http.Request) {
	a.templates.ExecuteTemplate(w, "products.html", nil)
}
func (a *App) appointmentPage(w http.ResponseWriter, r *http.Request) {
	a.templates.ExecuteTemplate(w, "appointment.html", nil)
}
func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	a.templates.ExecuteTemplate(w, "login.html", nil)
}

func jsonWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (a *App) listProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("category")
	var rows pgx.Rows
	var err error
	if q != "" {
		rows, err = a.db.Query(r.Context(), "SELECT id,name,category,description,price,stock,image_url,active FROM products WHERE active=true AND stock>0 AND category=$1 ORDER BY created_at DESC", q)
	} else {
		rows, err = a.db.Query(r.Context(), "SELECT id,name,category,description,price,stock,image_url,active FROM products WHERE active=true AND stock>0 ORDER BY created_at DESC")
	}
	if err != nil {
		jsonWrite(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	out := []Product{}
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.Description, &p.Price, &p.Stock, &p.ImageURL, &p.Active); err != nil {
			jsonWrite(w, 500, map[string]string{"error": "read error"})
			return
		}
		out = append(out, p)
	}
	jsonWrite(w, 200, out)
}
func (a *App) listAdminProducts(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	stockFilter := strings.TrimSpace(r.URL.Query().Get("stock"))
	where := []string{"1=1"}
	args := []any{}
	arg := 1
	if q != "" {
		where = append(where, fmt.Sprintf("(name ILIKE $%d OR description ILIKE $%d)", arg, arg))
		args = append(args, "%"+q+"%")
		arg++
	}
	if category != "" && category != "All" {
		where = append(where, fmt.Sprintf("category=$%d", arg))
		args = append(args, category)
		arg++
	}
	if stockFilter == "In Stock" {
		where = append(where, "stock > 0 AND active=true")
	} else if stockFilter == "Sold Out" {
		where = append(where, "stock = 0")
	} else if stockFilter == "Published" {
		where = append(where, "active=true AND stock > 0")
	} else if stockFilter == "Archived" {
		where = append(where, "active=false")
	}
	rows, err := a.db.Query(r.Context(), "SELECT id,name,category,description,price,stock,image_url,active FROM products WHERE "+strings.Join(where, " AND ")+" ORDER BY updated_at DESC, created_at DESC", args...)
	if err != nil {
		jsonWrite(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	out := []Product{}
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.Description, &p.Price, &p.Stock, &p.ImageURL, &p.Active); err == nil {
			out = append(out, p)
		}
	}
	jsonWrite(w, 200, out)
}

func (a *App) getProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		jsonWrite(w, 400, map[string]string{"error": "invalid id"})
		return
	}
	var p Product
	err = a.db.QueryRow(r.Context(), "SELECT id,name,category,description,price,stock,image_url,active FROM products WHERE id=$1", id).Scan(&p.ID, &p.Name, &p.Category, &p.Description, &p.Price, &p.Stock, &p.ImageURL, &p.Active)
	if err == pgx.ErrNoRows {
		jsonWrite(w, 404, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		jsonWrite(w, 500, map[string]string{"error": "database error"})
		return
	}
	jsonWrite(w, 200, p)
}
func (a *App) createProduct(w http.ResponseWriter, r *http.Request) {
	var p Product
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil || strings.TrimSpace(p.Name) == "" {
		jsonWrite(w, 400, map[string]string{"error": "invalid product"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Category = strings.TrimSpace(p.Category)
	allowedCategories := map[string]bool{"Bridal": true, "Non-Bridal": true, "Kids": true, "Sarees": true, "Blouses": true}
	if !allowedCategories[p.Category] {
		jsonWrite(w, 400, map[string]string{"error": "invalid category"})
		return
	}
	if p.Price < 0 || p.Stock < 0 {
		jsonWrite(w, 400, map[string]string{"error": "price and stock cannot be negative"})
		return
	}
	p.ID = uuid.New()
	// Stock of zero is never published to customers. This prevents sold-out
	// items from appearing in the public collection automatically.
	p.Active = p.Active && p.Stock > 0
	_, err := a.db.Exec(r.Context(), "INSERT INTO products(id,name,category,description,price,stock,image_url,active) VALUES($1,$2,$3,$4,$5,$6,$7,$8)", p.ID, p.Name, p.Category, p.Description, p.Price, p.Stock, p.ImageURL, p.Active)
	if err != nil {
		jsonWrite(w, 400, map[string]string{"error": "could not create product"})
		return
	}
	jsonWrite(w, 201, p)
}

func (a *App) updateProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		jsonWrite(w, 400, map[string]string{"error": "invalid id"})
		return
	}
	var p Product
	if json.NewDecoder(r.Body).Decode(&p) != nil {
		jsonWrite(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Category = strings.TrimSpace(p.Category)
	allowedCategories := map[string]bool{"Bridal": true, "Non-Bridal": true, "Kids": true, "Sarees": true, "Blouses": true}
	if p.Name == "" || !allowedCategories[p.Category] || p.Price < 0 || p.Stock < 0 {
		jsonWrite(w, 400, map[string]string{"error": "invalid product values"})
		return
	}
	active := p.Active && p.Stock > 0
	_, err = a.db.Exec(r.Context(), "UPDATE products SET name=$1,category=$2,description=$3,price=$4,stock=$5,image_url=$6,active=$7,updated_at=NOW() WHERE id=$8", p.Name, p.Category, p.Description, p.Price, p.Stock, p.ImageURL, active, id)
	if err != nil {
		jsonWrite(w, 400, map[string]string{"error": "could not update"})
		return
	}
	jsonWrite(w, 200, map[string]any{"message": "updated", "active": active})
}

func (a *App) deleteProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		jsonWrite(w, 400, map[string]string{"error": "invalid id"})
		return
	}

	// Remove the actual collection record, not just hide/archive it.
	// This makes the item disappear from the admin collection list as well
	// as from the public website.
	var imageURL string
	err = a.db.QueryRow(r.Context(), "SELECT image_url FROM products WHERE id=$1", id).Scan(&imageURL)
	if err == pgx.ErrNoRows {
		jsonWrite(w, 404, map[string]string{"error": "collection item not found"})
		return
	}
	if err != nil {
		jsonWrite(w, 500, map[string]string{"error": "could not find collection item"})
		return
	}

	_, err = a.db.Exec(r.Context(), "DELETE FROM products WHERE id=$1", id)
	if err != nil {
		jsonWrite(w, 500, map[string]string{"error": "could not remove collection item"})
		return
	}

	// Product images are stored as /uploads/products/<uuid>.<ext>.
	// Delete the matching local image too, so removed collection items do
	// not leave orphaned files behind.
	if imageURL != "" {
		cleanURL := strings.TrimPrefix(imageURL, "/")
		if strings.HasPrefix(cleanURL, "uploads/products/") {
			_ = os.Remove(filepath.FromSlash(cleanURL))
		}
	}

	jsonWrite(w, 200, map[string]string{"message": "collection item permanently removed"})
}

func (a *App) uploadProductImage(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		jsonWrite(w, 400, map[string]string{"error": "invalid product id"})
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		jsonWrite(w, 400, map[string]string{"error": "image is too large or invalid"})
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		jsonWrite(w, 400, map[string]string{"error": "image file is required"})
		return
	}
	defer file.Close()
	if header.Size > 5<<20 {
		jsonWrite(w, 400, map[string]string{"error": "image must be 5 MB or smaller"})
		return
	}
	contentType := header.Header.Get("Content-Type")
	allowedTypes := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}
	ext, ok := allowedTypes[contentType]
	if !ok {
		jsonWrite(w, 400, map[string]string{"error": "only JPG, PNG, and WebP images are allowed"})
		return
	}
	if err := os.MkdirAll("uploads/products", 0755); err != nil {
		jsonWrite(w, 500, map[string]string{"error": "could not create upload directory"})
		return
	}
	filename := id.String() + ext
	path := filepath.Join("uploads", "products", filename)
	out, err := os.Create(path)
	if err != nil {
		jsonWrite(w, 500, map[string]string{"error": "could not save image"})
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		jsonWrite(w, 500, map[string]string{"error": "could not save image"})
		return
	}
	imageURL := "/uploads/products/" + filename
	_, err = a.db.Exec(r.Context(), "UPDATE products SET image_url=$1,updated_at=NOW() WHERE id=$2", imageURL, id)
	if err != nil {
		jsonWrite(w, 500, map[string]string{"error": "image saved but product could not be updated"})
		return
	}
	jsonWrite(w, 200, map[string]string{"image_url": imageURL})
}

func (a *App) createAppointment(w http.ResponseWriter, r *http.Request) {
	var ap Appointment
	if json.NewDecoder(r.Body).Decode(&ap) != nil {
		jsonWrite(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if ap.Name == "" || ap.Phone == "" || ap.AppointmentDate == "" || ap.AppointmentTime == "" {
		jsonWrite(w, 400, map[string]string{"error": "name, phone, date and time are required"})
		return
	}
	ap.ID = uuid.New()
	_, err := a.db.Exec(r.Context(), "INSERT INTO appointments(id,name,phone,email,appointment_type,appointment_date,appointment_time,message) VALUES($1,$2,$3,$4,$5,$6,$7,$8)", ap.ID, ap.Name, ap.Phone, ap.Email, ap.AppointmentType, ap.AppointmentDate, ap.AppointmentTime, ap.Message)
	if err != nil {
		jsonWrite(w, 400, map[string]string{"error": "could not book appointment"})
		return
	}

	// The appointment is safely stored first. Email is sent asynchronously so the
	// customer is redirected immediately even if SMTP is temporarily unavailable.
	go a.notify.sendAppointmentEmails(ap)
	a.broadcastAppointment(ap)

	// Free WhatsApp option: generate a click-to-WhatsApp URL with the
	// appointment details pre-filled. WhatsApp itself still requires the user
	// to press Send; there is no paid API involved.
	whatsappURL := ""
	smsURL := ""
	emailURL := ""
	if number := normalizeWhatsAppNumber(os.Getenv("WHATSAPP_NUMBER")); number != "" {
		message := appointmentMessage(ap)
		encodedMessage := url.QueryEscape(strings.ReplaceAll(message, "\n", "\r\n"))
		whatsappURL = "https://wa.me/" + number + "?text=" + encodedMessage
		// Free SMS option: open the phone's messaging app with the
		// appointment details pre-filled. The user still presses Send.
		smsURL = "sms:+" + number + "?body=" + encodedMessage
	}
	if notifyEmail := strings.TrimSpace(os.Getenv("NOTIFY_EMAIL")); notifyEmail != "" {
		message := appointmentMessage(ap)
		emailURL = "mailto:" + url.QueryEscape(notifyEmail) + "?subject=" + url.QueryEscape("New appointment - "+ap.Name) + "&body=" + url.QueryEscape(message)
	}

	jsonWrite(w, 201, map[string]any{
		"message":      "Appointment request received",
		"redirect":     "/",
		"whatsapp_url": whatsappURL,
		"sms_url":      smsURL,
		"email_url":    emailURL,
	})
}

func normalizeWhatsAppNumber(number string) string {
	number = strings.TrimSpace(number)
	var b strings.Builder
	for _, r := range number {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func newNotifier() *Notifier {
	port, _ := strconv.Atoi(getenv("SMTP_PORT", "587"))
	return &Notifier{
		smtpHost:     os.Getenv("SMTP_HOST"),
		smtpPort:     port,
		smtpUser:     os.Getenv("SMTP_USER"),
		smtpPassword: os.Getenv("SMTP_PASSWORD"),
		mailFrom:     getenv("MAIL_FROM", os.Getenv("SMTP_USER")),
		notifyEmail:  getenv("NOTIFY_EMAIL", os.Getenv("ADMIN_EMAIL")),
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func appointmentMessage(ap Appointment) string {
	return fmt.Sprintf("New appointment at Bhairava Boutique\n\nName: %s\nPhone: %s\nEmail: %s\nType: %s\nDate: %s\nTime: %s\nMessage: %s", ap.Name, ap.Phone, ap.Email, ap.AppointmentType, ap.AppointmentDate, ap.AppointmentTime, ap.Message)
}

func customerMessage(ap Appointment) string {
	return fmt.Sprintf("Hi %s,\n\nYour appointment request with Bhairava Boutique was received for %s %s (%s). We will contact you shortly.\n\nThank you!", ap.Name, ap.AppointmentDate, ap.AppointmentTime, ap.AppointmentType)
}

func (n *Notifier) sendAppointmentEmails(ap Appointment) {
	if n.smtpHost == "" || n.smtpUser == "" || n.smtpPassword == "" || n.mailFrom == "" {
		log.Println("Email notifications disabled: configure SMTP_HOST, SMTP_USER, SMTP_PASSWORD and MAIL_FROM")
		return
	}
	if n.notifyEmail != "" {
		if err := n.sendEmail(n.notifyEmail, "New appointment - "+ap.Name, appointmentMessage(ap)); err != nil {
			log.Printf("Admin email notification failed: %v", err)
		}
	}
	if ap.Email != "" {
		if err := n.sendEmail(ap.Email, "Bhairava Boutique appointment request received", customerMessage(ap)); err != nil {
			log.Printf("Customer email notification failed: %v", err)
		}
	}
}

func (n *Notifier) sendCustomerStatusEmail(ap Appointment, status, description string) {
	if ap.Email == "" || n.smtpHost == "" || n.smtpUser == "" || n.smtpPassword == "" || n.mailFrom == "" {
		return
	}
	if err := n.sendEmail(ap.Email, "Bhairava Boutique - "+status, customerStatusMessage(ap, status, description)); err != nil {
		log.Printf("Customer status email failed: %v", err)
	}
}

func (n *Notifier) sendEmail(to, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", n.smtpHost, n.smtpPort)
	message := "From: " + n.mailFrom + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" + body + "\r\n"
	host := n.smtpHost
	auth := smtp.PlainAuth("", n.smtpUser, n.smtpPassword, host)
	if n.smtpPort == 465 {
		tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return err
		}
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		defer client.Close()
		if err = client.Auth(auth); err != nil {
			return err
		}
		if err = client.Mail(n.mailFrom); err != nil {
			return err
		}
		if err = client.Rcpt(to); err != nil {
			return err
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		if _, err = w.Write([]byte(message)); err != nil {
			return err
		}
		if err = w.Close(); err != nil {
			return err
		}
		return client.Quit()
	}
	return smtp.SendMail(addr, auth, n.mailFrom, []string{to}, []byte(message))
}

func (a *App) broadcastAppointment(ap Appointment) {
	b, err := json.Marshal(map[string]any{"type": "new_appointment", "appointment": ap})
	if err != nil {
		return
	}
	a.clientsMu.Lock()
	defer a.clientsMu.Unlock()
	for ch := range a.clients {
		select {
		case ch <- b:
		default:
		}
	}
}

func (a *App) adminEvents(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if !a.validToken(token) {
		jsonWrite(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := make(chan []byte, 8)
	a.clientsMu.Lock()
	a.clients[ch] = struct{}{}
	a.clientsMu.Unlock()
	defer func() { a.clientsMu.Lock(); delete(a.clients, ch); close(ch); a.clientsMu.Unlock() }()
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	for {
		select {
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-time.After(25 * time.Second):
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (a *App) validToken(token string) bool {
	if token == "" {
		return false
	}
	t, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) { return []byte(os.Getenv("JWT_SECRET")), nil })
	return err == nil && t.Valid
}

func (a *App) listAppointments(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	dateFilter := strings.TrimSpace(r.URL.Query().Get("date"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 10
	}
	offset := (page - 1) * limit

	where := []string{"1=1"}
	args := []any{}
	arg := 1
	if q != "" {
		where = append(where, fmt.Sprintf("(name ILIKE $%d OR phone ILIKE $%d OR email ILIKE $%d OR appointment_type ILIKE $%d OR message ILIKE $%d)", arg, arg, arg, arg, arg))
		args = append(args, "%"+q+"%")
		arg++
	}
	if statusFilter != "" && statusFilter != "All" {
		where = append(where, fmt.Sprintf("status = $%d", arg))
		args = append(args, statusFilter)
		arg++
	}
	if dateFilter != "" {
		where = append(where, fmt.Sprintf("appointment_date = $%d", arg))
		args = append(args, dateFilter)
		arg++
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := a.db.QueryRow(r.Context(), "SELECT COUNT(*) FROM appointments WHERE "+whereSQL, args...).Scan(&total); err != nil {
		jsonWrite(w, 500, map[string]string{"error": "database error"})
		return
	}
	query := "SELECT id,name,phone,email,appointment_type,appointment_date::text,appointment_time::text,message,status,notification_description FROM appointments WHERE " + whereSQL + " ORDER BY appointment_date DESC, appointment_time DESC LIMIT $" + strconv.Itoa(arg) + " OFFSET $" + strconv.Itoa(arg+1)
	args = append(args, limit, offset)
	rows, err := a.db.Query(r.Context(), query, args...)
	if err != nil {
		jsonWrite(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var x Appointment
		if err := rows.Scan(&x.ID, &x.Name, &x.Phone, &x.Email, &x.AppointmentType, &x.AppointmentDate, &x.AppointmentTime, &x.Message, &x.Status, &x.NotificationDescription); err != nil {
			continue
		}
		out = append(out, map[string]any{"id": x.ID, "name": x.Name, "phone": x.Phone, "email": x.Email, "appointment_type": x.AppointmentType, "appointment_date": x.AppointmentDate, "appointment_time": x.AppointmentTime, "message": x.Message, "status": x.Status, "notification_description": x.NotificationDescription, "whatsapp_url": customerWhatsAppURL(x, x.Status, x.NotificationDescription), "sms_url": customerSMSURL(x, x.Status, x.NotificationDescription), "email_url": customerEmailURL(x, x.Status, x.NotificationDescription)})
	}
	pages := (total + limit - 1) / limit
	jsonWrite(w, 200, map[string]any{"appointments": out, "page": page, "limit": limit, "total": total, "pages": pages})
}
func (a *App) updateAppointmentStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		jsonWrite(w, 400, map[string]string{"error": "invalid appointment id"})
		return
	}
	var in struct {
		Status      string `json:"status"`
		Description string `json:"description"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		jsonWrite(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	in.Status = strings.TrimSpace(in.Status)
	in.Description = strings.TrimSpace(in.Description)
	if in.Status != "Ready" && in.Status != "Completed" {
		in.Description = ""
	}
	allowed := map[string]bool{"Pending": true, "In Progress": true, "Ready": true, "Completed": true, "Cancelled": true}
	if !allowed[in.Status] {
		jsonWrite(w, 400, map[string]string{"error": "invalid status"})
		return
	}
	var ap Appointment
	if (in.Status == "Ready" || in.Status == "Completed") && in.Description == "" {
		jsonWrite(w, 400, map[string]string{"error": "notification description is required for Ready or Completed"})
		return
	}
	err = a.db.QueryRow(r.Context(), `UPDATE appointments SET status=$1, notification_description=$2 WHERE id=$3
		RETURNING id,name,phone,email,appointment_type,appointment_date::text,appointment_time::text,message,status,notification_description`, in.Status, in.Description, id).
		Scan(&ap.ID, &ap.Name, &ap.Phone, &ap.Email, &ap.AppointmentType, &ap.AppointmentDate, &ap.AppointmentTime, &ap.Message, &ap.Status, &ap.NotificationDescription)
	if err == pgx.ErrNoRows {
		jsonWrite(w, 404, map[string]string{"error": "appointment not found"})
		return
	}
	if err != nil {
		jsonWrite(w, 500, map[string]string{"error": "could not update appointment"})
		return
	}

	// Only customer-facing milestone statuses trigger a customer notification, and only with the admin-entered description.
	if (in.Status == "Ready" || in.Status == "Completed") && in.Description != "" {
		go a.notify.sendCustomerStatusEmail(ap, in.Status, in.Description)
	}
	a.broadcastAppointmentStatus(ap)

	jsonWrite(w, 200, map[string]any{
		"appointment":  ap,
		"whatsapp_url": customerWhatsAppURL(ap, in.Status, in.Description),
		"sms_url":      customerSMSURL(ap, in.Status, in.Description),
		"email_url":    customerEmailURL(ap, in.Status, in.Description),
	})
}

func customerStatusMessage(ap Appointment, status, description string) string {
	return fmt.Sprintf("Bhairava Boutique\n\nHello %s,\n\n%s\n\nAppointment date: %s\nAppointment time: %s\nType: %s\n\nThank you,\nBhairava Boutique", ap.Name, description, ap.AppointmentDate, ap.AppointmentTime, ap.AppointmentType)
}

func customerWhatsAppURL(ap Appointment, status, description string) string {
	if description == "" {
		return ""
	}
	n := normalizeCustomerNumber(ap.Phone)
	if n == "" {
		return ""
	}
	return "https://wa.me/" + n + "?text=" + url.QueryEscape(strings.ReplaceAll(customerStatusMessage(ap, status, description), "\n", "\r\n"))
}
func customerSMSURL(ap Appointment, status, description string) string {
	if description == "" {
		return ""
	}
	n := normalizeCustomerNumber(ap.Phone)
	if n == "" {
		return ""
	}
	return "sms:+" + n + "?body=" + url.QueryEscape(strings.ReplaceAll(customerStatusMessage(ap, status, description), "\n", "\r\n"))
}
func customerEmailURL(ap Appointment, status, description string) string {
	if description == "" || strings.TrimSpace(ap.Email) == "" {
		return ""
	}
	return "mailto:" + url.QueryEscape(strings.TrimSpace(ap.Email)) + "?subject=" + url.QueryEscape("Bhairava Boutique - "+status) + "&body=" + url.QueryEscape(customerStatusMessage(ap, status, description))
}

func normalizeCustomerNumber(number string) string {
	number = normalizeWhatsAppNumber(number)
	if len(number) == 10 {
		return "91" + number
	}
	if strings.HasPrefix(number, "00") {
		return strings.TrimPrefix(number, "00")
	}
	return number
}

func (a *App) broadcastAppointmentStatus(ap Appointment) {
	b, err := json.Marshal(map[string]any{"type": "appointment_status", "appointment": ap})
	if err != nil {
		return
	}
	a.clientsMu.Lock()
	defer a.clientsMu.Unlock()
	for ch := range a.clients {
		select {
		case ch <- b:
		default:
		}
	}
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		jsonWrite(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	email := os.Getenv("ADMIN_EMAIL")
	pass := os.Getenv("ADMIN_PASSWORD")
	if in.Email != email || in.Password != pass {
		jsonWrite(w, 401, map[string]string{"error": "invalid credentials"})
		return
	}
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		jsonWrite(w, 500, map[string]string{"error": "JWT_SECRET is not configured"})
		return
	}
	claims := Claims{Email: email, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		jsonWrite(w, 500, map[string]string{"error": "token error"})
		return
	}
	jsonWrite(w, 200, map[string]string{"token": token})
}
func (a *App) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			jsonWrite(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		if !a.validToken(strings.TrimPrefix(h, "Bearer ")) {
			jsonWrite(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

var _ = bcrypt.CompareHashAndPassword
