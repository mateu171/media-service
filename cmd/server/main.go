package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	mediapb "media-service/proto"
)

const listenAddr = ":50051"

// job — стан завдання у пам'яті сервера.
type job struct {
	ID          string
	Filename    string
	Format      mediapb.MediaFormat
	Quality     int32
	Status      string
	Progress    int32
	CreatedAt   int64
	CompletedAt int64
}

type server struct {
	mediapb.UnimplementedMediaServiceServer

	mu   sync.RWMutex
	jobs map[string]*job
}

func newServer() *server {
	return &server{jobs: make(map[string]*job)}
}

// newUUID генерує UUID v4 без зовнішніх залежностей.
func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func (s *server) SubmitJob(ctx context.Context, req *mediapb.ProcessMediaRequest) (*mediapb.JobResponse, error) {
	if req.GetFilename() == "" {
		return nil, status.Error(codes.InvalidArgument, "filename is required")
	}
	if req.GetTargetFormat() == mediapb.MediaFormat_MEDIA_FORMAT_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "target_format is required")
	}
	if q := req.GetQualityLevel(); q < 1 || q > 100 {
		return nil, status.Error(codes.InvalidArgument, "quality_level must be in range 1..100")
	}

	id, err := newUUID()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "cannot generate job id: %v", err)
	}

	j := &job{
		ID:        id,
		Filename:  req.GetFilename(),
		Format:    req.GetTargetFormat(),
		Quality:   req.GetQualityLevel(),
		Status:    "QUEUED",
		CreatedAt: time.Now().Unix(),
	}

	s.mu.Lock()
	s.jobs[id] = j
	s.mu.Unlock()

	log.Printf("[STORAGE] Job registered: ID=%s Filename=%s TargetFormat=%s Status=%s",
		j.ID, j.Filename, j.Format, j.Status)

	return &mediapb.JobResponse{JobId: j.ID, Status: j.Status, CreatedAt: j.CreatedAt}, nil
}

func (s *server) GetJobStatus(ctx context.Context, req *mediapb.GetJobStatusRequest) (*mediapb.JobStatusResponse, error) {
	s.mu.RLock()
	j, ok := s.jobs[req.GetJobId()]
	var resp *mediapb.JobStatusResponse
	if ok {
		resp = &mediapb.JobStatusResponse{
			JobId:           j.ID,
			Status:          j.Status,
			ProgressPercent: j.Progress,
			CompletedAt:     j.CompletedAt,
		}
	}
	s.mu.RUnlock()

	if !ok {
		return nil, status.Errorf(codes.NotFound, "job %q not found", req.GetJobId())
	}
	return resp, nil
}

type stage struct {
	percent int32
	name    string
}

var stages = []stage{
	{25, "Demuxing input stream"},
	{50, "Encoding video frames"},
	{75, "Remuxing audio and headers"},
	{100, "Process completed successfully"},
}

func (s *server) TrackProgress(req *mediapb.GetJobStatusRequest, stream mediapb.MediaService_TrackProgressServer) error {
	jobID := req.GetJobId()

	s.mu.RLock()
	_, ok := s.jobs[jobID]
	s.mu.RUnlock()
	if !ok {
		return status.Errorf(codes.NotFound, "job %q not found", jobID)
	}

	log.Printf("[STREAM] Client connected to track progress for JobID=%s", jobID)

	s.mu.Lock()
	s.jobs[jobID].Status = "PROCESSING"
	s.mu.Unlock()

	for _, st := range stages {
		select {
		case <-stream.Context().Done():
			log.Printf("[STREAM] Client canceled tracking for JobID=%s", jobID)
			return status.Error(codes.Canceled, "Client canceled tracking")
		case <-time.After(800 * time.Millisecond):
		}

		done := st.percent == 100

		s.mu.Lock()
		j := s.jobs[jobID]
		j.Progress = st.percent
		if done {
			j.Status = "COMPLETED"
			j.CompletedAt = time.Now().Unix()
		}
		s.mu.Unlock()

		update := &mediapb.ProgressUpdate{
			JobId:           jobID,
			CurrentStage:    st.name,
			ProgressPercent: st.percent,
			IsCompleted:     done,
		}
		if err := stream.Send(update); err != nil {
			return err
		}
		log.Printf("[STREAM] JobID=%s -> Sent stage %q (%d%%)", jobID, st.name, st.percent)
	}

	log.Printf("[STREAM] Progress tracking finished for JobID=%s", jobID)
	return nil
}

func main() {
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("[SERVER] failed to listen on %s: %v", listenAddr, err)
	}

	grpcServer := grpc.NewServer()
	mediapb.RegisterMediaServiceServer(grpcServer, newServer())

	log.Printf("[SERVER] Starting Media Processing gRPC service on %s...", listenAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("[SERVER] failed to serve: %v", err)
	}
}
