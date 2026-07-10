package chat

import (
	"fmt"
	"log/slog"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/help"
	"github.com/ayn2op/tview/keybind"
	"github.com/ayn2op/tview/picker"
)

// commonEmojis is the built-in unicode set shown for every channel; guild
// custom emojis are appended when the selected channel belongs to a guild.
var commonEmojis = []struct{ name, emoji string }{
	{"thumbsup", "👍"},
	{"thumbsdown", "👎"},
	{"heart", "❤️"},
	{"joy", "😂"},
	{"smile", "😄"},
	{"laughing", "😆"},
	{"sweat_smile", "😅"},
	{"wink", "😉"},
	{"grin", "😁"},
	{"open_mouth", "😮"},
	{"cry", "😢"},
	{"sob", "😭"},
	{"rage", "😡"},
	{"thinking", "🤔"},
	{"shrug", "🤷"},
	{"facepalm", "🤦"},
	{"clap", "👏"},
	{"ok_hand", "👌"},
	{"wave", "👋"},
	{"pray", "🙏"},
	{"muscle", "💪"},
	{"raised_hands", "🙌"},
	{"eyes", "👀"},
	{"salute", "🫡"},
	{"tada", "🎉"},
	{"rocket", "🚀"},
	{"fire", "🔥"},
	{"100", "💯"},
	{"white_check_mark", "✅"},
	{"x", "❌"},
	{"question", "❓"},
	{"exclamation", "❗"},
	{"warning", "⚠️"},
	{"star", "⭐"},
	{"sparkles", "✨"},
	{"zap", "⚡"},
	{"bug", "🐛"},
	{"skull", "💀"},
	{"clown", "🤡"},
}

type reactionOption struct {
	label string
	emoji discord.APIEmoji
	// me reports whether the current user already reacted with this emoji;
	// selecting it removes the reaction instead of adding one.
	me bool
}

type reactionsPicker struct {
	*picker.Model
	cfg  *config.Config
	chat *Model

	options   []reactionOption
	channelID discord.ChannelID
	messageID discord.MessageID
}

var _ help.KeyMap = (*reactionsPicker)(nil)

func newReactionsPicker(cfg *config.Config, chat *Model) *reactionsPicker {
	rp := &reactionsPicker{Model: picker.NewModel(), cfg: cfg, chat: chat}
	ConfigurePicker(rp.Model, cfg, "React")
	return rp
}

func (rp *reactionsPicker) update(message discord.Message) {
	rp.channelID = message.ChannelID
	rp.messageID = message.ID
	rp.options = rp.options[:0]

	seen := make(map[discord.APIEmoji]struct{})

	// Existing reactions first for quick toggling.
	for _, reaction := range message.Reactions {
		name := reaction.Emoji.Name
		if reaction.Emoji.ID.IsValid() {
			name = ":" + name + ":"
		}

		label := fmt.Sprintf("%s %d", name, reaction.Count)
		if reaction.Me {
			label += " - remove my reaction"
		}

		emoji := reaction.Emoji.APIString()
		seen[emoji] = struct{}{}
		rp.options = append(rp.options, reactionOption{label: label, emoji: emoji, me: reaction.Me})
	}

	for _, common := range commonEmojis {
		emoji := discord.APIEmoji(common.emoji)
		if _, ok := seen[emoji]; ok {
			continue
		}
		rp.options = append(rp.options, reactionOption{
			label: common.emoji + " :" + common.name + ":",
			emoji: emoji,
		})
	}

	if message.GuildID.IsValid() {
		emojis, err := rp.chat.state.Cabinet.Emojis(message.GuildID)
		if err != nil {
			slog.Error("failed to get emojis from state", "err", err, "guild_id", message.GuildID)
		}

		for _, emoji := range emojis {
			apiEmoji := emoji.APIString()
			if _, ok := seen[apiEmoji]; ok {
				continue
			}
			rp.options = append(rp.options, reactionOption{
				label: ":" + emoji.Name + ":",
				emoji: apiEmoji,
			})
		}
	}

	items := make(picker.Items, 0, len(rp.options))
	for i, option := range rp.options {
		items = append(items, picker.Item{
			Text:       option.label,
			FilterText: option.label,
			Reference:  i,
		})
	}
	rp.Model.SetItems(items)
}

func (rp *reactionsPicker) close() tview.Cmd {
	rp.chat.RemoveLayer(reactionsPickerLayerName)
	return tview.SetFocus(rp.chat.messagesList)
}

func (rp *reactionsPicker) react(option reactionOption) tview.Cmd {
	state := rp.chat.state
	channelID, messageID := rp.channelID, rp.messageID
	return func() tview.Msg {
		var err error
		if option.me {
			err = state.Unreact(channelID, messageID, option.emoji)
		} else {
			err = state.React(channelID, messageID, option.emoji)
		}
		if err != nil {
			slog.Error("failed to update reaction", "err", err, "channel_id", channelID, "message_id", messageID, "emoji", option.emoji)
		}
		return nil
	}
}

func (rp *reactionsPicker) Update(msg tview.Msg) tview.Cmd {
	switch msg := msg.(type) {
	case picker.SelectedMsg:
		index, ok := msg.Reference.(int)
		if !ok || index < 0 || index >= len(rp.options) {
			return nil
		}
		return tview.Batch(rp.react(rp.options[index]), rp.close())
	case picker.CancelMsg:
		return rp.close()
	}

	return rp.Model.Update(msg)
}

func (rp *reactionsPicker) ShortHelp() []keybind.Keybind {
	cfg := rp.cfg.Keybinds.Picker
	return []keybind.Keybind{cfg.SelectUp.Keybind, cfg.SelectDown.Keybind, cfg.Select.Keybind, cfg.Cancel.Keybind}
}

func (rp *reactionsPicker) FullHelp() [][]keybind.Keybind {
	cfg := rp.cfg.Keybinds.Picker
	return [][]keybind.Keybind{
		{cfg.SelectUp.Keybind, cfg.SelectDown.Keybind, cfg.SelectTop.Keybind, cfg.SelectBottom.Keybind},
		{cfg.Select.Keybind, cfg.Cancel.Keybind},
	}
}
