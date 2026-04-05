package chat

import (
	"context"
	"log/slog"

	"github.com/ayn2op/tview"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/gdamore/tcell/v3"
)

func (m *Model) openState() tview.Cmd {
	return func() tview.Event {
		if err := m.state.Open(context.Background()); err != nil {
			slog.Error("failed to open chat state", "err", err)
			return nil
		}
		return nil
	}
}

func (m *Model) closeState() tview.Cmd {
	return func() tview.Event {
		if m.state != nil {
			if err := m.state.Close(); err != nil {
				slog.Error("failed to close the session", "err", err)
				return nil
			}
		}
		return nil
	}
}

type gatewayEvent struct {
	tcell.EventTime
	gateway.Event
}

func (m *Model) listen() tview.Cmd {
	return func() tview.Event {
		return &gatewayEvent{Event: <-m.events}
	}
}

type channelLoadedEvent struct {
	tcell.EventTime
	Channel  discord.Channel
	Messages []discord.Message
}

func newChannelLoadedEvent(channel discord.Channel, messages []discord.Message) *channelLoadedEvent {
	return &channelLoadedEvent{Channel: channel, Messages: messages}
}

type olderMessagesLoadedEvent struct {
	tcell.EventTime
	ChannelID discord.ChannelID
	Older     []discord.Message
}

func newOlderMessagesLoadedEvent(channelID discord.ChannelID, older []discord.Message) *olderMessagesLoadedEvent {
	return &olderMessagesLoadedEvent{ChannelID: channelID, Older: older}
}

type LogoutEvent struct{ tcell.EventTime }

func (m *Model) logout() tview.Cmd {
	return func() tview.Event {
		return &LogoutEvent{}
	}
}

type QuitEvent struct{ tcell.EventTime }

type closeLayerEvent struct {
	tcell.EventTime
	name string
}

func closeLayer(name string) tview.Cmd {
	return func() tview.Event {
		return &closeLayerEvent{name: name}
	}
}
