package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
)

// --- Protocol messages ---

// IncomingMessage is a JSON message received from the mobile client.
type IncomingMessage struct {
	Type        string            `json:"type"`
	Token       string            `json:"token,omitempty"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	UserPrompt  string            `json:"userPrompt,omitempty"`
	Runner      string            `json:"runner,omitempty"`
	Model       string            `json:"model,omitempty"`
	Mode        string            `json:"mode,omitempty"`
	TaskID      string            `json:"taskId,omitempty"`
	Input       string            `json:"input,omitempty"`
	Source      string            `json:"source,omitempty"`
	ProjectName string            `json:"projectName,omitempty"`
	Images      []ImageAttachment `json:"images,omitempty"`

	PlacementKind      string `json:"placementKind,omitempty"`
	ForceCloud         bool   `json:"forceCloud,omitempty"`
	ForceRelaySource   bool   `json:"forceRelaySource,omitempty"`
	AllowLocalFallback bool   `json:"allowLocalFallback,omitempty"`
}

// OutgoingMessage is a JSON message sent back to the mobile client.
type OutgoingMessage struct {
	Type          string                 `json:"type"`
	DeviceName    string                 `json:"deviceName,omitempty"`
	TaskID        string                 `json:"taskId,omitempty"`
	PendingTaskID string                 `json:"pendingTaskId,omitempty"`
	Status        string                 `json:"status,omitempty"`
	Text          string                 `json:"text,omitempty"`
	Final         bool                   `json:"final,omitempty"`
	Tasks         []TaskInfo             `json:"tasks,omitempty"`
	Message       string                 `json:"message,omitempty"`
	Action        string                 `json:"action,omitempty"`
	Reason        string                 `json:"reason,omitempty"`
	Placement     *TaskPlacementMetadata `json:"placement,omitempty"`
	Activation    map[string]any         `json:"activation,omitempty"`
}

// QUICServer wraps a QUIC listener and dispatches incoming messages to a
// TaskManager.
type QUICServer struct {
	port        int
	taskManager *TaskManager
	authToken   string // expected token from mobile clients
	deviceName  string
	deviceID    string
	convexURL   string
	listener    *quic.Listener
}

// NewQUICServer creates a QUICServer.
func NewQUICServer(port int, authToken, deviceName string, tm *TaskManager, opts ...QUICServerOption) *QUICServer {
	s := &QUICServer{
		port:        port,
		taskManager: tm,
		authToken:   authToken,
		deviceName:  deviceName,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

type QUICServerOption func(*QUICServer)

func WithQUICPlacementBackend(convexURL, deviceID string) QUICServerOption {
	return func(s *QUICServer) {
		s.convexURL = convexURL
		s.deviceID = deviceID
	}
}

func (s *QUICServer) placementClient() (*taskPlacementBackendClient, error) {
	if s == nil {
		return nil, fmt.Errorf("server unavailable")
	}
	return newTaskPlacementBackendClient(s.convexURL, s.authToken)
}

func (s *QUICServer) previewTaskPlacement(ctx context.Context, meta taskPlacementRecordRequest) (*TaskPlacementMetadata, error) {
	client, err := s.placementClient()
	if err != nil {
		return nil, err
	}
	meta.TaskID = ""
	return client.postTaskPlacement(ctx, "/tasks/placement/preview", meta)
}

func (s *QUICServer) recordTaskPlacement(ctx context.Context, taskID string, meta taskPlacementRecordRequest) (*TaskPlacementMetadata, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("missing backend auth")
	}
	client, err := s.placementClient()
	if err != nil {
		return nil, err
	}
	meta.TaskID = strings.TrimSpace(taskID)
	return client.postTaskPlacement(ctx, "/tasks/placement/record", meta)
}

func (s *QUICServer) activateTaskPlacement(ctx context.Context, placementID, taskID string) (map[string]any, error) {
	client, err := s.placementClient()
	if err != nil {
		return nil, err
	}
	return client.activateTaskPlacement(ctx, placementID, taskID)
}

func (s *QUICServer) cloudRequiredMessage(ctx context.Context, msg IncomingMessage, meta taskPlacementRecordRequest) (*OutgoingMessage, *TaskPlacementMetadata) {
	previewPlacement, err := s.previewTaskPlacement(ctx, meta)
	if err != nil {
		log.Printf("[placement] QUIC preview skipped before task create: %v", err)
		return nil, nil
	}
	if msg.AllowLocalFallback || !shouldDeferLocalTaskForPlacement(previewPlacement, s.deviceID) {
		return nil, previewPlacement
	}
	pendingTaskID := newPendingCloudTaskID()
	recordedPlacement := previewPlacement
	if placement, perr := s.recordTaskPlacement(ctx, pendingTaskID, meta); perr != nil {
		log.Printf("[placement] QUIC pending record skipped for %s: %v", pendingTaskID, perr)
	} else if placement != nil {
		recordedPlacement = placement
	}
	var activation map[string]any
	if recordedPlacement != nil && (recordedPlacement.PlacementID != "" || pendingTaskID != "") {
		if result, aerr := s.activateTaskPlacement(ctx, recordedPlacement.PlacementID, pendingTaskID); aerr != nil {
			activation = activationMapFromError(aerr)
			log.Printf("[placement] QUIC activation skipped for %s: %v", pendingTaskID, aerr)
		} else {
			activation = result
		}
	}
	return &OutgoingMessage{
		Type:          "error",
		Message:       "cloud workspace required",
		Action:        "cloud_workspace_required",
		PendingTaskID: pendingTaskID,
		Placement:     recordedPlacement,
		Activation:    activation,
		Reason:        "placement selected a Cloud Workspace that is not ready on this agent; keep the prompt client-side, wait for activation, then dispatch to the assigned workspace",
	}, recordedPlacement
}

func (s *QUICServer) recordCreatedTaskPlacement(ctx context.Context, task *Task, meta taskPlacementRecordRequest) {
	if s == nil || task == nil {
		return
	}
	placement, err := s.recordTaskPlacement(ctx, task.ID, meta)
	if err != nil {
		log.Printf("[placement] QUIC record skipped for task %s: %v", task.ID, err)
		return
	}
	if placement == nil {
		return
	}
	s.taskManager.mu.Lock()
	task.Placement = placement
	s.taskManager.persist()
	s.taskManager.mu.Unlock()
}

func quicTaskPlacementRequest(msg IncomingMessage, source, workDir, targetDeviceID string) taskPlacementRecordRequest {
	return taskPlacementRequestFromTaskBody(taskPlacementRequestInput{
		KindHint:         msg.PlacementKind,
		Title:            msg.Title,
		Description:      msg.Description,
		Source:           source,
		Runner:           msg.Runner,
		ProjectName:      msg.ProjectName,
		WorkDir:          workDir,
		TargetDeviceID:   targetDeviceID,
		ForceCloud:       msg.ForceCloud,
		ForceRelaySource: msg.ForceRelaySource,
	})
}

func quicCloudRequiredErrorFromMessage(resp OutgoingMessage) *CloudWorkspaceRequiredError {
	if resp.Type != "error" || strings.TrimSpace(resp.Action) != "cloud_workspace_required" {
		return nil
	}
	return &CloudWorkspaceRequiredError{
		PendingTaskID: strings.TrimSpace(resp.PendingTaskID),
		Placement:     resp.Placement,
		Activation:    resp.Activation,
		Reason:        firstNonEmpty(resp.Reason, resp.Message),
	}
}

// Start begins listening for QUIC connections.
func (s *QUICServer) Start(ctx context.Context) error {
	tlsCfg, err := loadOrGenerateTLS()
	if err != nil {
		return fmt.Errorf("TLS setup: %w", err)
	}

	addr := fmt.Sprintf("0.0.0.0:%d", s.port)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve addr: %w", err)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}

	tr := &quic.Transport{Conn: conn}
	listener, err := tr.Listen(tlsCfg, &quic.Config{
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 15 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("quic listen: %w", err)
	}
	s.listener = listener

	log.Printf("QUIC server listening on %s", addr)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		session, err := listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // shutting down
			}
			log.Printf("accept error: %v", err)
			continue
		}
		go s.handleConnection(ctx, session)
	}
}

// handleConnection processes all streams on a single QUIC connection.
func (s *QUICServer) handleConnection(ctx context.Context, conn quic.Connection) {
	defer conn.CloseWithError(0, "bye")

	authenticated := false

	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("accept stream error: %v", err)
			return
		}
		go s.handleStream(ctx, stream, &authenticated)
	}
}

// handleStream reads a single JSON message from a stream and sends a response.
func (s *QUICServer) handleStream(ctx context.Context, stream quic.Stream, authenticated *bool) {
	defer stream.Close()

	data, err := io.ReadAll(io.LimitReader(stream, 1<<20)) // 1 MB limit
	if err != nil {
		log.Printf("read stream: %v", err)
		return
	}

	var msg IncomingMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendMessage(stream, OutgoingMessage{Type: "error", Message: "invalid JSON"})
		return
	}

	// Auth message must come first.
	if msg.Type == "auth" {
		if !secretEqual(msg.Token, s.authToken) {
			s.sendMessage(stream, OutgoingMessage{Type: "error", Message: "invalid token"})
			return
		}
		*authenticated = true
		s.sendMessage(stream, OutgoingMessage{Type: "auth_ok", DeviceName: s.deviceName})
		return
	}

	if !*authenticated {
		s.sendMessage(stream, OutgoingMessage{Type: "error", Message: "not authenticated"})
		return
	}

	switch msg.Type {
	case "task_create":
		s.handleTaskCreate(stream, msg)
	case "task_stop":
		s.handleTaskStop(stream, msg)
	case "task_list":
		s.handleTaskList(stream)
	case "task_continue":
		s.handleTaskContinue(stream, msg)
	default:
		s.sendMessage(stream, OutgoingMessage{Type: "error", Message: fmt.Sprintf("unknown message type: %s", msg.Type)})
	}
}

func (s *QUICServer) handleTaskCreate(stream quic.Stream, msg IncomingMessage) {
	source := msg.Source
	if source == "" {
		source = "mobile"
	}
	placementMeta := quicTaskPlacementRequest(msg, source, s.taskManager.workDir, s.deviceID)
	var previewPlacement *TaskPlacementMetadata
	if cloudMsg, placement := s.cloudRequiredMessage(context.Background(), msg, placementMeta); cloudMsg != nil {
		s.sendMessage(stream, *cloudMsg)
		return
	} else {
		previewPlacement = placement
	}
	task, err := s.taskManager.CreateTaskWithOptions(msg.Title, msg.Description, msg.Model, source, msg.Runner, "", msg.Images, TaskCreateOptions{
		InitialUserPrompt: msg.UserPrompt,
		Mode:              msg.Mode,
		Placement:         previewPlacement,
	})
	if err != nil {
		s.sendMessage(stream, OutgoingMessage{Type: "error", Message: err.Error()})
		return
	}
	s.recordCreatedTaskPlacement(context.Background(), task, placementMeta)

	s.sendMessage(stream, OutgoingMessage{
		Type:   "task_created",
		TaskID: task.ID,
		Status: string(task.Status),
	})

	// Stream output in the background if the task has an output channel.
	go s.streamTaskOutput(stream, task)
}

func (s *QUICServer) handleTaskStop(stream quic.Stream, msg IncomingMessage) {
	if err := s.taskManager.StopTask(msg.TaskID); err != nil {
		s.sendMessage(stream, OutgoingMessage{Type: "error", Message: err.Error()})
		return
	}
	s.sendMessage(stream, OutgoingMessage{
		Type:   "task_output",
		TaskID: msg.TaskID,
		Text:   "Task stopped.",
		Final:  true,
	})
}

func (s *QUICServer) handleTaskList(stream quic.Stream) {
	tasks := s.taskManager.ListTasks()
	s.sendMessage(stream, OutgoingMessage{
		Type:  "task_list",
		Tasks: tasks,
	})
}

func (s *QUICServer) handleTaskContinue(stream quic.Stream, msg IncomingMessage) {
	task, err := s.taskManager.ResumeTaskWithOptions(msg.TaskID, msg.Input, msg.Images, TaskResumeOptions{
		RunnerID: msg.Runner,
		Model:    msg.Model,
		Mode:     msg.Mode,
	})
	if err != nil {
		s.sendMessage(stream, OutgoingMessage{Type: "error", Message: err.Error()})
		return
	}
	s.sendMessage(stream, OutgoingMessage{
		Type:   "task_created",
		TaskID: task.ID,
		Status: string(task.Status),
	})
	go s.streamTaskOutput(stream, task)
}

// streamTaskOutput reads from the task's output channel and sends each line
// back over the QUIC stream. This keeps the stream open until the task ends.
func (s *QUICServer) streamTaskOutput(stream quic.Stream, task *Task) {
	for line := range task.outputCh {
		s.sendMessage(stream, OutgoingMessage{
			Type:   "task_output",
			TaskID: task.ID,
			Text:   line,
		})
	}
	// Send final message.
	s.sendMessage(stream, OutgoingMessage{
		Type:   "task_output",
		TaskID: task.ID,
		Text:   task.Output,
		Final:  true,
	})
}

// sendMessage writes a JSON message to a QUIC stream.
func (s *QUICServer) sendMessage(stream quic.Stream, msg OutgoingMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("marshal response: %v", err)
		return
	}
	data = append(data, '\n')
	if _, err := stream.Write(data); err != nil {
		log.Printf("write stream: %v", err)
	}
}

// --- TLS helpers ---

// loadOrGenerateTLS loads TLS certs from ~/.yaver/ or generates a self-signed
// certificate on first run.
func loadOrGenerateTLS() (*tls.Config, error) {
	certPath, err := TLSCertPath()
	if err != nil {
		return nil, err
	}
	keyPath, err := TLSKeyPath()
	if err != nil {
		return nil, err
	}

	// Try loading existing certs.
	if _, err := os.Stat(certPath); err == nil {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err == nil {
			return &tls.Config{
				Certificates: []tls.Certificate{cert},
				NextProtos:   []string{"yaver-p2p"},
			}, nil
		}
		log.Printf("existing TLS cert invalid, regenerating: %v", err)
	}

	// Generate self-signed certificate.
	log.Println("Generating self-signed TLS certificate...")
	cert, err := generateSelfSignedCert(certPath, keyPath)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"yaver-p2p"},
	}, nil
}

func generateSelfSignedCert(certPath, keyPath string) (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Yaver Desktop Agent"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}

	// Write cert PEM.
	certFile, err := os.Create(certPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create cert file: %w", err)
	}
	defer certFile.Close()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return tls.Certificate{}, fmt.Errorf("encode cert: %w", err)
	}

	// Write key PEM.
	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("marshal key: %w", err)
	}
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create key file: %w", err)
	}
	defer keyFile.Close()
	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER}); err != nil {
		return tls.Certificate{}, fmt.Errorf("encode key: %w", err)
	}

	return tls.LoadX509KeyPair(certPath, keyPath)
}
