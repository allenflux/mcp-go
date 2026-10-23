package transport

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func initializeInProcessCloseTest(t *testing.T, tr *InProcessTransport) {
	t.Helper()
	require.NoError(t, tr.Start(t.Context()))
	response, err := tr.SendRequest(t.Context(), JSONRPCRequest{
		JSONRPC: mcp.JSONRPC_VERSION, ID: mcp.NewRequestId(1), Method: "initialize",
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_LEGACY_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "test", Version: "1"},
		},
	})
	require.NoError(t, err)
	require.Nil(t, response.Error)
}

func TestInProcessTransportRejectsMessagesAfterClose(t *testing.T) {
	for _, started := range []bool{false, true} {
		name := "before start"
		if started {
			name = "after initialize"
		}
		t.Run(name, func(t *testing.T) {
			s := server.NewMCPServer("test", "1")
			tools, notifications := 0, 0
			s.AddTool(mcp.NewTool("count"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				tools++
				return mcp.NewToolResultText("counted"), nil
			})
			notification := mcp.JSONRPCNotification{JSONRPC: mcp.JSONRPC_VERSION,
				Notification: mcp.Notification{Method: "notifications/roots/list_changed"}}
			s.AddNotificationHandler(notification.Method, func(context.Context, mcp.JSONRPCNotification) { notifications++ })
			tr := NewInProcessTransport(s)
			request := JSONRPCRequest{JSONRPC: mcp.JSONRPC_VERSION, ID: mcp.NewRequestId(2),
				Method: "tools/call", Params: mcp.CallToolParams{Name: "count"}}
			want := 0
			if started {
				initializeInProcessCloseTest(t, tr)
				response, err := tr.SendRequest(t.Context(), request)
				require.NoError(t, err)
				require.Nil(t, response.Error)
				require.NoError(t, tr.SendNotification(t.Context(), notification))
				require.Equal(t, 1, tools)
				require.Equal(t, 1, notifications)
				want = 1
			}
			require.NoError(t, tr.Close())
			require.NoError(t, tr.Close())
			response, err := tr.SendRequest(t.Context(), request)
			assert.ErrorIs(t, err, ErrTransportClosed)
			assert.Nil(t, response)
			assert.ErrorIs(t, tr.SendNotification(t.Context(), notification), ErrTransportClosed)
			assert.Equal(t, want, tools, "closed transport invoked a tool")
			assert.Equal(t, want, notifications, "closed transport invoked a notification handler")
			assert.ErrorIs(t, tr.Start(t.Context()), ErrTransportClosed)
		})
	}
}

func TestInProcessTransportHandlerCanCloseTransport(t *testing.T) {
	for _, method := range []string{"request", "notification"} {
		t.Run(method, func(t *testing.T) {
			s := server.NewMCPServer("test", "1")
			tr := NewInProcessTransport(s)
			s.AddTool(mcp.NewTool("close"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("closed"), tr.Close()
			})
			notification := mcp.JSONRPCNotification{JSONRPC: mcp.JSONRPC_VERSION,
				Notification: mcp.Notification{Method: "notifications/roots/list_changed"}}
			s.AddNotificationHandler(notification.Method, func(context.Context, mcp.JSONRPCNotification) { _ = tr.Close() })
			initializeInProcessCloseTest(t, tr)
			done := make(chan error, 1)
			go func() {
				if method == "notification" {
					done <- tr.SendNotification(t.Context(), notification)
					return
				}
				response, err := tr.SendRequest(t.Context(), JSONRPCRequest{
					JSONRPC: mcp.JSONRPC_VERSION, ID: mcp.NewRequestId(2),
					Method: "tools/call", Params: mcp.CallToolParams{Name: "close"},
				})
				if err == nil && response.Error != nil {
					err = response.Error.AsError()
				}
				done <- err
			}()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(time.Second):
				t.Fatal("handler deadlocked closing its transport")
			}
			require.ErrorIs(t, tr.Start(t.Context()), ErrTransportClosed)
		})
	}
}
