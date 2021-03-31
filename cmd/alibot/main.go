package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"time"

	// "github.com/davecgh/go-spew/spew"
	tg "github.com/go-telegram-bot-api/telegram-bot-api"
	_ "modernc.org/sqlite"
)

var TELEGRAM_TOKEN = os.Getenv("BOT_TOKEN")
var bot *tg.BotAPI
var updatesChan tg.UpdatesChannel
var linkRegexp = regexp.MustCompile(`(?m)https:\/\/a\.aliexpress\.com\/(.*)`)
var msgActor = make(chan(tg.Chattable), 10)
var bgContext = context.Background()
var cancel = func() {}

var signals = make(chan(os.Signal))

var state *AppState = nil

var VERBOSE bool
var STATE string
func handleSignals() {
    for range signals {
        state.Flush()
        log.Printf("Finalizando...")
        cancel()
    }
}

const help = `
Esse bot tem como objetivo ajudar todos os que estão participando das pechinchas do aliexpress.
- Você não pode impulsionar o próprio link
- Você pode impulsionar até 3 links a cada 24h
- Você precisa de n impulsos para conseguir o desconto do produto
- Cada conta pode estar associada a só um celular e um só celular por conta
Nisso o bot une o útil ao agradável
- Use /next para visualizar os links das outras pessoas para você impulsionar. Dica: Para dispositivos secundários manda o que esse comando te entregar para o email da conta google esse celular secundário
O comando vai te entregar uma lista de links, se o link expirou ou já foi concedido tem um comando depois do link, só clicar nele.
Se alguém marcar seu link como expirado o bot te avisa e você pode readicionar pelo comando enviado.
- Os outros vão acabar fazendo isso com seus links e todo mundo sai ganhando.
- Eu quero um drone: https://a.aliexpress.com/_mOKDdtH
`

func init() {
    signal.Notify(signals, os.Interrupt, os.Kill)
    go handleSignals()
    bgContext, cancel = context.WithCancel(bgContext)
    flag.BoolVar(&VERBOSE, "v", false, "Print more data about what is going on")
    flag.StringVar(&STATE, "d", "./database.json", "Where to save the state")
    flag.Parse()
    var err error

    state = NewAppState(STATE)
    err = state.Load()
    if err != nil {
        panic(err)
    }

    bot, err = tg.NewBotAPI(TELEGRAM_TOKEN)
    if err != nil {
        panic(err)
    }
    if VERBOSE {
        bot.Debug = true
    }
    updatesChan, err = bot.GetUpdatesChan(tg.UpdateConfig{
        Limit: 5,
    })
    if err != nil {
        panic(err)
    }
    go flushActorHandler()
    go msgActorHandler()
}

func flushActorHandler() {
    ticker := time.Tick(10*time.Second)
    for {
        select {
        case <-ticker:
            log.Printf("Running scheduled state flush...")
            state.Flush()
        case <-bgContext.Done():
            return
        }
    }
}

func msgActorHandler() {
    for {
        select {
            case msg :=<-msgActor:
                go func(msg tg.Chattable) {
                    for {
                        _, err := bot.Send(msg)
                        if err != nil {
                            log.Printf("Message send failed: %s", err)
                            time.Sleep(time.Second)
                            continue
                        } else {
                            break
                        }
                    }
                }(msg)
            case <-bgContext.Done():
                return
        }
    }
}



func extractID(text string) string {
    // https://regex101.com/r/tbuVV2/1
    res := linkRegexp.FindStringSubmatch(text)
    if res == nil {
        return ""
    }
    if len(res) != 2 {
        return ""
    }
    return res[1]
}



func handleUpdate(u tg.Update) {
    // spew.Dump(u)
    if (u.Message == nil) {
        return
    }
    chat_id := u.Message.From.ID
    {
        user := u.Message.From.String()
        prettyMsg := strings.ReplaceAll(u.Message.Text, "\n", "\\n")
        log.Printf("<%s> %s", user, prettyMsg)

    }
    cmd := u.Message.Command()
    sendError := func (err error) {
        if err != nil {
            msgActor<-tg.NewMessage(int64(chat_id), fmt.Sprintf("ERRO: %s", err))
        }
    }
    if (u.Message.Text == "") {
        sendError(errors.New("Apenas texto é suportado"))
        return
    }
    if strings.HasPrefix(cmd, "ok") {
        id := strings.TrimPrefix(cmd, "ok")
        owner := state.GetAliIDOwner(id)
        if owner == nil {
            sendError(errors.New("AliID não encontrado"))
            return
        }
        if fmt.Sprintf("%d", owner) != id {
            msgActor<-tg.NewMessage(int64(*owner), fmt.Sprintf(`Link removido da lista por @%s
            LINK: https://a.aliexpress.com/%s
            READICIONAR: /add%s`, u.Message.From.UserName, id, id))
        }
        state.DoneLink(id)
        return
    }
    if strings.HasPrefix(cmd, "add") && len(cmd) >= 4 {
        id := strings.TrimPrefix(cmd, "add")
        state.AddLink(chat_id, id)
        msgActor<-tg.NewMessage(int64(chat_id), fmt.Sprintf("Link readicionado. Remover /ok%s", id))

        return
    }
    if cmd == "next" {
        notCompleted := state.GetNotCompleted(chat_id)
        if len(notCompleted) == 0 {
            sendError(errors.New("Nenhum link na fila"))
            return
        }
        for k, v := range notCompleted {
            notCompleted[k] = fmt.Sprintf("https://a.aliexpress.com/%s\nSe expirado ou já concedido: /ok%s", v, v)
        }
        msgActor<-tg.NewMessage(int64(chat_id), strings.Join(notCompleted, "\n"))
        return
    }
    if cmd == "flush" {
        sendError(state.Flush())
        return
    }
    link := extractID(u.Message.Text)
    if link == "" {
        msgActor<-tg.NewMessage(int64(u.Message.From.ID), "Se era pra ser um link, ou comando, ele não foi reconhecido")
    } else {
        state.AddLink(u.Message.From.ID, link)
        msgActor<-tg.NewMessage(int64(u.Message.From.ID), fmt.Sprintf("Link https://a.aliexpress.com/%s adicionado com sucesso.\nREMOVER: /ok%s", link, link))
        return
    }
    msgActor<-tg.NewMessage(int64(chat_id), help)
}

func main() {
    me, err := bot.GetMe()
    if err != nil {
        panic(err)
    }
    log.Printf("Iniciando bot @%s", me.UserName)
    for {
        select {
        case update:=<-updatesChan:
            go handleUpdate(update)
        case <-bgContext.Done():
            return
        }
    }
}

type AppState struct {
    filename string
    sync.Mutex
    data map[string]LinkMetadata
}

type LinkMetadata struct {
    Owner int
    IsEnabled bool
}

func NewAppState(filename string) *AppState {
    return &AppState{
        filename: filename,
        data: map[string]LinkMetadata{},
    }
}

func (a *AppState) Flush() error {
    newFile := fmt.Sprintf("%s.tmp", a.filename)
    a.Lock()
    defer a.Unlock()
    f, err := os.Create(newFile)
    if err != nil {
        return err
    }
    json.NewEncoder(f).Encode(a.data)
    err = f.Close()
    if err != nil {
        return err
    }
    return os.Rename(newFile, a.filename)
}

func (a *AppState) Load() error {
    a.Lock()
    defer a.Unlock()
    _, err := os.Stat(a.filename)
    if err != nil {
        f, err := os.Create(a.filename)
        if err != nil {
            return err
        }
        json.NewEncoder(f).Encode(a.data)
        return f.Close()
    }
    f, err := os.Open(a.filename)
    if err != nil {
        return err
    }
    defer f.Close()
    return json.NewDecoder(f).Decode(&a.data)
}

func (a *AppState) AddLink(from int, ali_id string) {
    a.Lock()
    defer a.Unlock()
    item, ok := a.data[ali_id]
    if !ok {
        a.data[ali_id] = LinkMetadata{
            Owner: from,
            IsEnabled: true,
        }
    } else {
        item.IsEnabled = true
    }
}

func (a *AppState) DoneLink(ali_id string) {
    a.Lock()
    defer a.Unlock()
    item, ok := a.data[ali_id]
    if ok {
        item.IsEnabled = false
        a.data[ali_id] = item
    }
}

func (a *AppState) CountLinks() int {
    return len(a.data)
}

func (a *AppState) GetNotCompleted(user int) []string {
    a.Lock()
    defer a.Unlock()
    ret := make([]string, 0, 3)
    for k, v := range a.data {
        if v.IsEnabled == true && v.Owner != user {
            ret = append(ret, k)
        }
        if len(ret) >= 10 {
            return ret
        }
    }
    return ret
}

func (a *AppState) GetAliIDOwner(ali_id string) *int {
    a.Lock()
    defer a.Unlock()
    item, ok := a.data[ali_id]
    if !ok {
        return nil
    } else {
        return &item.Owner
    }
}


