package main

import (
	_ "github.com/alterfo/kb/internal/connectors/blog"
	_ "github.com/alterfo/kb/internal/connectors/chat/mattermost"
	_ "github.com/alterfo/kb/internal/connectors/chat/slack"
	_ "github.com/alterfo/kb/internal/connectors/chat/telegram"
	_ "github.com/alterfo/kb/internal/connectors/discord"
	_ "github.com/alterfo/kb/internal/connectors/file"
	_ "github.com/alterfo/kb/internal/connectors/github"
	_ "github.com/alterfo/kb/internal/connectors/gitlab"
	_ "github.com/alterfo/kb/internal/connectors/mcp"
	_ "github.com/alterfo/kb/internal/connectors/searchapi"
	_ "github.com/alterfo/kb/internal/connectors/tracker/kaiten"
	_ "github.com/alterfo/kb/internal/connectors/tracker/trello"
	_ "github.com/alterfo/kb/internal/connectors/tracker/weeek"
	_ "github.com/alterfo/kb/internal/connectors/tracker/yandex"
	_ "github.com/alterfo/kb/internal/connectors/tracker/youtrack"
	_ "github.com/alterfo/kb/internal/connectors/web"
	_ "github.com/alterfo/kb/internal/connectors/wiki"
)
