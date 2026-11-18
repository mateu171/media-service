package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	mediapb "media-service/proto"
)

const serverAddr = "localhost:50051"

func main() {
	fmt.Println("=== Media Processing Service CLI Client ===")

	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("[ERROR] cannot create client: %v", err)
	}
	defer conn.Close()

	client := mediapb.NewMediaServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Крок 1: реєстрація завдання.
	fmt.Println("[Step 1] Sending SubmitJob request...")
	job, err := client.SubmitJob(ctx, &mediapb.ProcessMediaRequest{
		ClientId:     "cli-client-1",
		Filename:     "input_video.mov",
		TargetFormat: mediapb.MediaFormat_MEDIA_FORMAT_MP4,
		QualityLevel: 80,
	})
	if err != nil {
		log.Fatalf("[ERROR] SubmitJob failed: %v", err)
	}
	fmt.Println("[SUCCESS] Job created successfully:")
	fmt.Printf("- Job ID: %s\n", job.GetJobId())
	fmt.Printf("- Initial State: %s\n", job.GetStatus())
	fmt.Printf("- Submitted At: %s\n", time.Unix(job.GetCreatedAt(), 0).Format("2006-01-02 15:04:05"))

	// Крок 2: Server Streaming.
	fmt.Println("[Step 2] Subscribing to Server Streaming: TrackProgress...")
	stream, err := client.TrackProgress(ctx, &mediapb.GetJobStatusRequest{JobId: job.GetJobId()})
	if err != nil {
		log.Fatalf("[ERROR] TrackProgress failed: %v", err)
	}
	for {
		upd, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			log.Fatalf("[ERROR] stream error: %v", err)
		}
		fmt.Printf("[%d%%] %s\n", upd.GetProgressPercent(), upd.GetCurrentStage())
	}

	// Крок 3: фінальний статус.
	fmt.Println("[Step 3] Checking final job status via GetJobStatus RPC...")
	st, err := client.GetJobStatus(ctx, &mediapb.GetJobStatusRequest{JobId: job.GetJobId()})
	if err != nil {
		log.Fatalf("[ERROR] GetJobStatus failed: %v", err)
	}
	fmt.Printf("- Current Status: %s\n", st.GetStatus())
	fmt.Printf("- Progress: %d%%\n", st.GetProgressPercent())
	fmt.Printf("- Completed At: %s\n", time.Unix(st.GetCompletedAt(), 0).Format("2006-01-02 15:04:05"))

	fmt.Println("=== Processing pipeline finished successfully ===")
}
