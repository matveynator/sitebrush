package serviceinstall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

type Options struct {
	Port        string
	StoragePath string
	DBType      string
	DBPath      string
	BinaryPath  string
	WorkingDir  string
	ServiceName string
	Language    string
	Input       io.Reader
	Output      io.Writer
}

var ErrCancelled = errors.New("service action cancelled by user")

type Result struct {
	OS          string
	Arch        string
	OSVersion   string
	InitSystem  string
	BinaryPath  string
	ServicePath string
	Commands    []string
}

type commandRunner func(context.Context, string, ...string) (string, error)

type runtimeProbe struct {
	goos        string
	goarch      string
	osVersion   string
	lookPath    func(string) (string, error)
	fileExists  func(string) bool
	dirExists   func(string) bool
	commandRuns commandRunner
}

type serviceManager struct {
	Name      string
	Priority  int
	Detect    func(runtimeProbe) bool
	Install   func(context.Context, runtimeProbe, installPlan) (Result, error)
	Uninstall func(context.Context, runtimeProbe, installPlan) (Result, error)
}

type installPlan struct {
	Options     Options
	ServiceName string
	BinaryPath  string
	WorkingDir  string
	ExecArgs    []string
}

type serviceCommand struct {
	Args         []string
	AllowFailure bool
}

type cliTheme struct {
	enabled bool
}

type serviceMetadata struct {
	Language    string `json:"language"`
	ServiceName string `json:"service_name"`
	Port        string `json:"port"`
	StoragePath string `json:"storage_path"`
	DBType      string `json:"db_type"`
	DBPath      string `json:"db_path"`
	BinaryPath  string `json:"binary_path"`
	WorkingDir  string `json:"working_dir"`
}

type cliText struct {
	LanguageName           string
	LanguageTitle          string
	LanguagePrompt         string
	InfoLabel              string
	DetectedLabel          string
	OSLabel                string
	ServiceSystemLabel     string
	InstallTitle           string
	InstallExplanation     string
	InstallReviewTitle     string
	InstallHelp            string
	InstallPrompt          string
	InstallDefaultAction   string
	InstallCancelled       string
	UninstallTitle         string
	UninstallExplanation   string
	KeepDataExplanation    string
	UninstallReviewTitle   string
	UninstallHelp          string
	UninstallPrompt        string
	UninstallDefaultAction string
	UninstallKeepOption    string
	UninstallRemoveOption  string
	UninstallSelectHelp    string
	UninstallCancelled     string
	InstallComplete        string
	UninstallComplete      string
	CommandsLabel          string
	BinaryLeftLabel        string
	ServiceNameLabel       string
	PortLabel              string
	StoragePathLabel       string
	DBPathLabel            string
	BinaryPathLabel        string
	WorkingDirLabel        string
	CommandLabel           string
	ServiceFileLabel       string
	DiskSpaceLabel         string
	DiskSpaceFreeOf        string
	DiskSpaceUnknown       string
	UnknownAction          string
}

var cliLanguageOrder = []string{"en", "ru", "de", "es", "fr", "it", "pt", "tr", "zh", "ja", "fa", "he", "fi", "kk", "mn", "sv"}

var cliTranslations = map[string]cliText{
	"en": {
		LanguageName: "English", LanguageTitle: "Language / Язык / Sprache / Idioma / Langue", LanguagePrompt: "Number/code", InfoLabel: "Info:", DetectedLabel: "Detected:", OSLabel: "OS", ServiceSystemLabel: "service system",
		InstallTitle: "Sitebrush service install", InstallExplanation: "Sitebrush will run in the background, start automatically after reboot, and start now. Use this when the computer or server must keep the site online without opening a terminal.",
		InstallReviewTitle: "Review before installing", InstallHelp: "Press Enter to continue. Type a number to change a setting. Type q to quit without changing the system.", InstallPrompt: "Continue, change setting number, or quit", InstallDefaultAction: "continue", InstallCancelled: "install cancelled by user",
		UninstallTitle: "Sitebrush service uninstall", UninstallExplanation: "Sitebrush will be stopped and removed from automatic startup.", KeepDataExplanation: "Your site files, database, and uploaded content are kept on disk.",
		UninstallReviewTitle: "Review before uninstalling", UninstallHelp: "Safe default: pressing Enter keeps the service installed. Use Left/Right to choose, then press Enter. Type 1 to change the service name. Type q to quit.", UninstallPrompt: "Keep service, type remove, change setting number, or quit", UninstallDefaultAction: "keep", UninstallKeepOption: "Keep service", UninstallRemoveOption: "Remove service", UninstallSelectHelp: "Left/Right selects. Enter confirms. 1 edits service name. q quits.", UninstallCancelled: "uninstall cancelled by user",
		InstallComplete: "Sitebrush installed as a service", UninstallComplete: "Sitebrush service removed from startup and stopped", CommandsLabel: "Commands", BinaryLeftLabel: "Installed binary left in place",
		ServiceNameLabel: "Service name", PortLabel: "Ports", StoragePathLabel: "Storage folder", DBPathLabel: "Database path", BinaryPathLabel: "Program file", WorkingDirLabel: "Working folder", CommandLabel: "Command", ServiceFileLabel: "Service file", DiskSpaceLabel: "Disk space", DiskSpaceFreeOf: "free of", DiskSpaceUnknown: "unknown", UnknownAction: "Unknown action",
	},
	"ru": {
		LanguageName: "Русский", LanguageTitle: "Выберите язык интерфейса", LanguagePrompt: "Номер языка или код", InfoLabel: "Информация:", DetectedLabel: "Обнаружено:", OSLabel: "ОС", ServiceSystemLabel: "система служб",
		InstallTitle: "Установка службы Sitebrush", InstallExplanation: "Sitebrush будет работать в фоне, запускаться автоматически после перезагрузки и запустится сразу сейчас. Это нужно, чтобы сайт оставался доступен без открытого терминала.",
		InstallReviewTitle: "Проверьте настройки перед установкой", InstallHelp: "Нажмите Enter, чтобы продолжить. Введите номер, чтобы изменить настройку. Введите q, чтобы выйти без изменений.", InstallPrompt: "Продолжить, изменить номер настройки или выйти", InstallDefaultAction: "продолжить", InstallCancelled: "установка отменена пользователем",
		UninstallTitle: "Удаление службы Sitebrush", UninstallExplanation: "Sitebrush будет остановлен и убран из автозапуска.", KeepDataExplanation: "Файлы сайта, база данных и загруженный контент останутся на диске.",
		UninstallReviewTitle: "Проверьте настройки перед удалением", UninstallHelp: "Безопасно по умолчанию: если нажать Enter, служба останется установленной. Стрелками влево/вправо выберите действие и нажмите Enter. Введите 1, чтобы изменить имя службы. Введите q, чтобы выйти.", UninstallPrompt: "Оставить службу, написать удалить, изменить номер настройки или выйти", UninstallDefaultAction: "оставить", UninstallKeepOption: "Оставить службу", UninstallRemoveOption: "Удалить службу", UninstallSelectHelp: "Стрелки влево/вправо выбирают действие. Enter подтверждает. 1 меняет имя службы. q выходит.", UninstallCancelled: "удаление отменено пользователем",
		InstallComplete: "Sitebrush установлен как системная служба", UninstallComplete: "Служба Sitebrush остановлена и убрана из автозапуска", CommandsLabel: "Команды", BinaryLeftLabel: "Установленный файл программы оставлен на месте",
		ServiceNameLabel: "Имя службы", PortLabel: "Порты", StoragePathLabel: "Папка данных", DBPathLabel: "Путь к базе данных", BinaryPathLabel: "Файл программы", WorkingDirLabel: "Рабочая папка", CommandLabel: "Команда запуска", ServiceFileLabel: "Файл службы", DiskSpaceLabel: "Место на диске", DiskSpaceFreeOf: "свободно из", DiskSpaceUnknown: "неизвестно", UnknownAction: "Неизвестное действие",
	},
	"de": {
		LanguageName: "Deutsch", InfoLabel: "Info:", DetectedLabel: "Erkannt:", ServiceSystemLabel: "Dienstsystem",
		InstallTitle: "Sitebrush-Dienst installieren", InstallExplanation: "Sitebrush lauft im Hintergrund, startet nach einem Neustart automatisch und wird jetzt gestartet.", InstallReviewTitle: "Vor der Installation prufen", InstallHelp: "Enter setzt fort. Eine Zahl andert eine Einstellung. q beendet ohne Anderungen.", InstallPrompt: "Fortfahren, Nummer andern oder beenden", InstallDefaultAction: "fortfahren", InstallCancelled: "Installation vom Benutzer abgebrochen",
		UninstallTitle: "Sitebrush-Dienst entfernen", UninstallExplanation: "Sitebrush wird gestoppt und aus dem Autostart entfernt.", KeepDataExplanation: "Websitedateien, Datenbank und Uploads bleiben auf der Festplatte.", UninstallReviewTitle: "Vor dem Entfernen prufen", UninstallHelp: "Enter entfernt den Dienst. 1 andert den Dienstnamen. q beendet.", UninstallPrompt: "Dienst entfernen, Nummer andern oder beenden", UninstallDefaultAction: "entfernen", UninstallCancelled: "Entfernen vom Benutzer abgebrochen",
		ServiceNameLabel: "Dienstname", PortLabel: "Ports", StoragePathLabel: "Datenordner", DBPathLabel: "Datenbankpfad", BinaryPathLabel: "Programmdatei", WorkingDirLabel: "Arbeitsordner", CommandLabel: "Befehl", ServiceFileLabel: "Dienstdatei", DiskSpaceLabel: "Speicherplatz", UnknownAction: "Unbekannte Aktion",
	},
	"es": {
		LanguageName: "Español", InfoLabel: "Informacion:", DetectedLabel: "Detectado:", ServiceSystemLabel: "sistema de servicio",
		InstallTitle: "Instalar servicio Sitebrush", InstallExplanation: "Sitebrush se ejecutara en segundo plano, arrancara tras reiniciar y se iniciara ahora.", InstallReviewTitle: "Revise antes de instalar", InstallHelp: "Pulse Enter para continuar. Escriba un numero para cambiar una opcion. Escriba q para salir.", InstallPrompt: "Continuar, cambiar numero o salir", InstallDefaultAction: "continuar", InstallCancelled: "instalacion cancelada por el usuario",
		UninstallTitle: "Desinstalar servicio Sitebrush", UninstallExplanation: "Sitebrush se detendra y se quitara del inicio automatico.", KeepDataExplanation: "Los archivos del sitio, la base de datos y las subidas se conservan.", UninstallReviewTitle: "Revise antes de desinstalar", UninstallHelp: "Pulse Enter para desinstalar el servicio. Escriba 1 para cambiar el nombre. Escriba q para salir.", UninstallPrompt: "Desinstalar servicio, cambiar numero o salir", UninstallDefaultAction: "desinstalar", UninstallCancelled: "desinstalacion cancelada por el usuario",
		ServiceNameLabel: "Nombre del servicio", PortLabel: "Puertos", StoragePathLabel: "Carpeta de datos", DBPathLabel: "Ruta de base de datos", BinaryPathLabel: "Archivo del programa", WorkingDirLabel: "Carpeta de trabajo", CommandLabel: "Comando", ServiceFileLabel: "Archivo de servicio", DiskSpaceLabel: "Espacio en disco", UnknownAction: "Accion desconocida",
	},
	"fr": {
		LanguageName: "Français", InfoLabel: "Info:", DetectedLabel: "Detecte:", ServiceSystemLabel: "systeme de service",
		InstallTitle: "Installer le service Sitebrush", InstallExplanation: "Sitebrush fonctionnera en arriere-plan, demarrera apres redemarrage et sera lance maintenant.", InstallReviewTitle: "Verifier avant installation", InstallHelp: "Entree continue. Un numero modifie un reglage. q quitte sans changement.", InstallPrompt: "Continuer, modifier un numero ou quitter", InstallDefaultAction: "continuer", InstallCancelled: "installation annulee par l'utilisateur",
		UninstallTitle: "Supprimer le service Sitebrush", UninstallExplanation: "Sitebrush sera arrete et retire du demarrage automatique.", KeepDataExplanation: "Les fichiers du site, la base de donnees et les televersements restent sur le disque.", UninstallReviewTitle: "Verifier avant suppression", UninstallHelp: "Entree supprime le service. 1 modifie le nom. q quitte.", UninstallPrompt: "Supprimer le service, modifier un numero ou quitter", UninstallDefaultAction: "supprimer", UninstallCancelled: "suppression annulee par l'utilisateur",
		ServiceNameLabel: "Nom du service", PortLabel: "Ports", StoragePathLabel: "Dossier des donnees", DBPathLabel: "Chemin de la base", BinaryPathLabel: "Fichier programme", WorkingDirLabel: "Dossier de travail", CommandLabel: "Commande", ServiceFileLabel: "Fichier du service", DiskSpaceLabel: "Espace disque", UnknownAction: "Action inconnue",
	},
	"it": {LanguageName: "Italiano", InfoLabel: "Info:", DetectedLabel: "Rilevato:", ServiceSystemLabel: "sistema servizi", InstallTitle: "Installazione servizio Sitebrush", InstallExplanation: "Sitebrush funzionera in background, partira dopo il riavvio e verra avviato ora.", InstallReviewTitle: "Controlla prima di installare", InstallHelp: "Premi Invio per continuare. Scrivi un numero per cambiare un'impostazione. Scrivi q per uscire.", InstallPrompt: "Continua, cambia numero o esci", InstallDefaultAction: "continua", InstallCancelled: "installazione annullata dall'utente", UninstallTitle: "Rimozione servizio Sitebrush", UninstallExplanation: "Sitebrush verra fermato e rimosso dall'avvio automatico.", KeepDataExplanation: "File del sito, database e contenuti caricati restano sul disco.", UninstallReviewTitle: "Controlla prima di rimuovere", UninstallHelp: "Premi Invio per rimuovere il servizio. Scrivi 1 per cambiare nome. Scrivi q per uscire.", UninstallPrompt: "Rimuovi servizio, cambia numero o esci", UninstallDefaultAction: "rimuovi", UninstallCancelled: "rimozione annullata dall'utente", ServiceNameLabel: "Nome servizio", PortLabel: "Porte", StoragePathLabel: "Cartella dati", DBPathLabel: "Percorso database", BinaryPathLabel: "File programma", WorkingDirLabel: "Cartella di lavoro", CommandLabel: "Comando", ServiceFileLabel: "File servizio", DiskSpaceLabel: "Spazio disco", UnknownAction: "Azione sconosciuta"},
	"pt": {LanguageName: "Português", InfoLabel: "Info:", DetectedLabel: "Detectado:", ServiceSystemLabel: "sistema de servico", InstallTitle: "Instalar servico Sitebrush", InstallExplanation: "Sitebrush rodara em segundo plano, iniciara apos reiniciar e sera iniciado agora.", InstallReviewTitle: "Revise antes de instalar", InstallHelp: "Pressione Enter para continuar. Digite um numero para alterar. Digite q para sair.", InstallPrompt: "Continuar, alterar numero ou sair", InstallDefaultAction: "continuar", InstallCancelled: "instalacao cancelada pelo usuario", UninstallTitle: "Remover servico Sitebrush", UninstallExplanation: "Sitebrush sera parado e removido da inicializacao automatica.", KeepDataExplanation: "Arquivos do site, banco de dados e uploads ficam no disco.", UninstallReviewTitle: "Revise antes de remover", UninstallHelp: "Pressione Enter para remover o servico. Digite 1 para alterar o nome. Digite q para sair.", UninstallPrompt: "Remover servico, alterar numero ou sair", UninstallDefaultAction: "remover", UninstallCancelled: "remocao cancelada pelo usuario", ServiceNameLabel: "Nome do servico", PortLabel: "Portas", StoragePathLabel: "Pasta de dados", DBPathLabel: "Caminho do banco", BinaryPathLabel: "Arquivo do programa", WorkingDirLabel: "Pasta de trabalho", CommandLabel: "Comando", ServiceFileLabel: "Arquivo do servico", DiskSpaceLabel: "Espaco em disco", UnknownAction: "Acao desconhecida"},
	"tr": {LanguageName: "Türkçe", InfoLabel: "Bilgi:", DetectedLabel: "Algilandi:", ServiceSystemLabel: "servis sistemi", InstallTitle: "Sitebrush servisini kur", InstallExplanation: "Sitebrush arka planda calisacak, yeniden baslatmadan sonra otomatik acilacak ve simdi baslatilacak.", InstallReviewTitle: "Kurulumdan once kontrol edin", InstallHelp: "Devam etmek icin Enter. Ayari degistirmek icin numara. Cikmak icin q.", InstallPrompt: "Devam et, numara degistir veya cik", InstallDefaultAction: "devam", InstallCancelled: "kurulum kullanici tarafindan iptal edildi", UninstallTitle: "Sitebrush servisini kaldir", UninstallExplanation: "Sitebrush durdurulacak ve otomatik baslangictan kaldirilacak.", KeepDataExplanation: "Site dosyalari, veritabani ve yuklemeler diskte kalir.", UninstallReviewTitle: "Kaldirmadan once kontrol edin", UninstallHelp: "Servisi kaldirmak icin Enter. Adi degistirmek icin 1. Cikmak icin q.", UninstallPrompt: "Servisi kaldir, numara degistir veya cik", UninstallDefaultAction: "kaldir", UninstallCancelled: "kaldirma kullanici tarafindan iptal edildi", ServiceNameLabel: "Servis adi", PortLabel: "Portlar", StoragePathLabel: "Veri klasoru", DBPathLabel: "Veritabani yolu", BinaryPathLabel: "Program dosyasi", WorkingDirLabel: "Calisma klasoru", CommandLabel: "Komut", ServiceFileLabel: "Servis dosyasi", DiskSpaceLabel: "Disk alani", UnknownAction: "Bilinmeyen islem"},
	"zh": {LanguageName: "中文", InfoLabel: "信息:", DetectedLabel: "检测到:", ServiceSystemLabel: "服务系统", InstallTitle: "安装 Sitebrush 服务", InstallExplanation: "Sitebrush 将在后台运行，重启后自动启动，并且现在立即启动。", InstallReviewTitle: "安装前检查", InstallHelp: "按 Enter 继续。输入数字修改设置。输入 q 退出且不更改系统。", InstallPrompt: "继续、输入设置编号或退出", InstallDefaultAction: "继续", InstallCancelled: "用户取消安装", UninstallTitle: "卸载 Sitebrush 服务", UninstallExplanation: "Sitebrush 将停止，并从自动启动中移除。", KeepDataExplanation: "站点文件、数据库和上传内容会保留在磁盘上。", UninstallReviewTitle: "卸载前检查", UninstallHelp: "按 Enter 卸载服务。输入 1 修改服务名。输入 q 退出。", UninstallPrompt: "卸载服务、输入设置编号或退出", UninstallDefaultAction: "卸载", UninstallCancelled: "用户取消卸载", ServiceNameLabel: "服务名", PortLabel: "端口", StoragePathLabel: "数据文件夹", DBPathLabel: "数据库路径", BinaryPathLabel: "程序文件", WorkingDirLabel: "工作文件夹", CommandLabel: "命令", ServiceFileLabel: "服务文件", DiskSpaceLabel: "磁盘空间", UnknownAction: "未知操作"},
	"ja": {LanguageName: "日本語", InfoLabel: "情報:", DetectedLabel: "検出:", ServiceSystemLabel: "サービス方式", InstallTitle: "Sitebrush サービスをインストール", InstallExplanation: "Sitebrush はバックグラウンドで動作し、再起動後に自動起動し、今すぐ起動します。", InstallReviewTitle: "インストール前の確認", InstallHelp: "Enter で続行。番号で設定変更。q で終了します。", InstallPrompt: "続行、設定番号を変更、または終了", InstallDefaultAction: "続行", InstallCancelled: "ユーザーがインストールを中止しました", UninstallTitle: "Sitebrush サービスを削除", UninstallExplanation: "Sitebrush を停止し、自動起動から外します。", KeepDataExplanation: "サイトファイル、データベース、アップロード内容はディスクに残ります。", UninstallReviewTitle: "削除前の確認", UninstallHelp: "Enter でサービス削除。1 でサービス名変更。q で終了します。", UninstallPrompt: "サービス削除、設定番号を変更、または終了", UninstallDefaultAction: "削除", UninstallCancelled: "ユーザーが削除を中止しました", ServiceNameLabel: "サービス名", PortLabel: "ポート", StoragePathLabel: "データフォルダ", DBPathLabel: "データベースパス", BinaryPathLabel: "プログラムファイル", WorkingDirLabel: "作業フォルダ", CommandLabel: "コマンド", ServiceFileLabel: "サービスファイル", DiskSpaceLabel: "ディスク容量", UnknownAction: "不明な操作"},
	"fa": {LanguageName: "فارسی", InfoLabel: "اطلاعات:", DetectedLabel: "تشخیص داده شد:", ServiceSystemLabel: "سامانه سرویس", InstallTitle: "نصب سرویس Sitebrush", InstallExplanation: "Sitebrush در پس زمینه اجرا می شود، بعد از راه اندازی دوباره خودکار شروع می شود و اکنون هم اجرا می شود.", InstallReviewTitle: "بررسی پیش از نصب", InstallHelp: "برای ادامه Enter بزنید. برای تغییر یک تنظیم شماره را وارد کنید. برای خروج q را وارد کنید.", InstallPrompt: "ادامه، تغییر شماره تنظیم یا خروج", InstallDefaultAction: "ادامه", InstallCancelled: "نصب توسط کاربر لغو شد", UninstallTitle: "حذف سرویس Sitebrush", UninstallExplanation: "Sitebrush متوقف می شود و از شروع خودکار حذف می شود.", KeepDataExplanation: "فایل های سایت، پایگاه داده و فایل های بارگذاری شده روی دیسک باقی می مانند.", UninstallReviewTitle: "بررسی پیش از حذف", UninstallHelp: "برای حذف سرویس Enter بزنید. برای تغییر نام سرویس 1 را وارد کنید. برای خروج q را وارد کنید.", UninstallPrompt: "حذف سرویس، تغییر شماره تنظیم یا خروج", UninstallDefaultAction: "حذف", UninstallCancelled: "حذف توسط کاربر لغو شد", ServiceNameLabel: "نام سرویس", PortLabel: "پورت ها", StoragePathLabel: "پوشه داده", DBPathLabel: "مسیر پایگاه داده", BinaryPathLabel: "فایل برنامه", WorkingDirLabel: "پوشه کاری", CommandLabel: "دستور", ServiceFileLabel: "فایل سرویس", DiskSpaceLabel: "فضای دیسک", UnknownAction: "عمل ناشناخته"},
	"he": {LanguageName: "עברית", InfoLabel: "מידע:", DetectedLabel: "זוהה:", ServiceSystemLabel: "מערכת שירותים", InstallTitle: "התקנת שירות Sitebrush", InstallExplanation: "Sitebrush ירוץ ברקע, יופעל אוטומטית אחרי אתחול ויופעל עכשיו.", InstallReviewTitle: "בדיקה לפני התקנה", InstallHelp: "Enter להמשך. מספר לשינוי הגדרה. q ליציאה ללא שינוי.", InstallPrompt: "המשך, שנה מספר הגדרה או צא", InstallDefaultAction: "המשך", InstallCancelled: "ההתקנה בוטלה על ידי המשתמש", UninstallTitle: "הסרת שירות Sitebrush", UninstallExplanation: "Sitebrush יעצר ויוסר מהפעלה אוטומטית.", KeepDataExplanation: "קבצי האתר, מסד הנתונים והעלאות ישארו בדיסק.", UninstallReviewTitle: "בדיקה לפני הסרה", UninstallHelp: "Enter להסרת השירות. 1 לשינוי שם השירות. q ליציאה.", UninstallPrompt: "הסר שירות, שנה מספר הגדרה או צא", UninstallDefaultAction: "הסר", UninstallCancelled: "ההסרה בוטלה על ידי המשתמש", ServiceNameLabel: "שם השירות", PortLabel: "פורטים", StoragePathLabel: "תיקיית נתונים", DBPathLabel: "נתיב מסד נתונים", BinaryPathLabel: "קובץ תוכנה", WorkingDirLabel: "תיקיית עבודה", CommandLabel: "פקודה", ServiceFileLabel: "קובץ שירות", DiskSpaceLabel: "שטח דיסק", UnknownAction: "פעולה לא ידועה"},
	"fi": {LanguageName: "Suomi", InfoLabel: "Tieto:", DetectedLabel: "Havaittu:", ServiceSystemLabel: "palvelujarjestelma", InstallTitle: "Asenna Sitebrush-palvelu", InstallExplanation: "Sitebrush toimii taustalla, kaynnistyy uudelleenkaynnistyksen jalkeen ja kaynnistetaan nyt.", InstallReviewTitle: "Tarkista ennen asennusta", InstallHelp: "Enter jatkaa. Numero muuttaa asetusta. q poistuu.", InstallPrompt: "Jatka, muuta numeroa tai poistu", InstallDefaultAction: "jatka", InstallCancelled: "kayttaja perui asennuksen", UninstallTitle: "Poista Sitebrush-palvelu", UninstallExplanation: "Sitebrush pysaytetaan ja poistetaan automaattisesta kaynnistyksesta.", KeepDataExplanation: "Sivuston tiedostot, tietokanta ja lataukset jaavat levylle.", UninstallReviewTitle: "Tarkista ennen poistoa", UninstallHelp: "Enter poistaa palvelun. 1 muuttaa palvelun nimea. q poistuu.", UninstallPrompt: "Poista palvelu, muuta numeroa tai poistu", UninstallDefaultAction: "poista", UninstallCancelled: "kayttaja perui poiston", ServiceNameLabel: "Palvelun nimi", PortLabel: "Portit", StoragePathLabel: "Datakansio", DBPathLabel: "Tietokannan polku", BinaryPathLabel: "Ohjelmatiedosto", WorkingDirLabel: "Tyokansio", CommandLabel: "Komento", ServiceFileLabel: "Palvelutiedosto", DiskSpaceLabel: "Levytila", UnknownAction: "Tuntematon toiminto"},
	"kk": {LanguageName: "Қазақша", InfoLabel: "Ақпарат:", DetectedLabel: "Анықталды:", ServiceSystemLabel: "қызмет жүйесі", InstallTitle: "Sitebrush қызметін орнату", InstallExplanation: "Sitebrush фонда жұмыс істейді, қайта жүктеуден кейін автоматты іске қосылады және қазір басталады.", InstallReviewTitle: "Орнату алдында тексеріңіз", InstallHelp: "Жалғастыру үшін Enter. Баптауды өзгерту үшін нөмір. Шығу үшін q.", InstallPrompt: "Жалғастыру, нөмірді өзгерту немесе шығу", InstallDefaultAction: "жалғастыру", InstallCancelled: "орнатуды пайдаланушы тоқтатты", UninstallTitle: "Sitebrush қызметін жою", UninstallExplanation: "Sitebrush тоқтатылады және автожүктеуден алынады.", KeepDataExplanation: "Сайт файлдары, дерекқор және жүктелген файлдар дискіде қалады.", UninstallReviewTitle: "Жою алдында тексеріңіз", UninstallHelp: "Қызметті жою үшін Enter. Атын өзгерту үшін 1. Шығу үшін q.", UninstallPrompt: "Қызметті жою, нөмірді өзгерту немесе шығу", UninstallDefaultAction: "жою", UninstallCancelled: "жоюды пайдаланушы тоқтатты", ServiceNameLabel: "Қызмет аты", PortLabel: "Порттар", StoragePathLabel: "Деректер қалтасы", DBPathLabel: "Дерекқор жолы", BinaryPathLabel: "Бағдарлама файлы", WorkingDirLabel: "Жұмыс қалтасы", CommandLabel: "Команда", ServiceFileLabel: "Қызмет файлы", DiskSpaceLabel: "Диск орны", UnknownAction: "Белгісіз әрекет"},
	"mn": {LanguageName: "Монгол", InfoLabel: "Мэдээлэл:", DetectedLabel: "Илэрсэн:", ServiceSystemLabel: "үйлчилгээний систем", InstallTitle: "Sitebrush үйлчилгээг суулгах", InstallExplanation: "Sitebrush далд ажиллаж, дахин асаасны дараа автоматаар эхэлж, одоо шууд асна.", InstallReviewTitle: "Суулгахаас өмнө шалгах", InstallHelp: "Үргэлжлүүлэх бол Enter. Тохиргоо өөрчлөх бол дугаар. Гарах бол q.", InstallPrompt: "Үргэлжлүүлэх, дугаар өөрчлөх эсвэл гарах", InstallDefaultAction: "үргэлжлүүлэх", InstallCancelled: "суулгалтыг хэрэглэгч цуцалсан", UninstallTitle: "Sitebrush үйлчилгээг устгах", UninstallExplanation: "Sitebrush зогсож, автоматаар эхлэхээс хасагдана.", KeepDataExplanation: "Сайтын файл, өгөгдлийн сан, байршуулсан контент диск дээр үлдэнэ.", UninstallReviewTitle: "Устгахаас өмнө шалгах", UninstallHelp: "Үйлчилгээ устгах бол Enter. Нэр өөрчлөх бол 1. Гарах бол q.", UninstallPrompt: "Үйлчилгээ устгах, дугаар өөрчлөх эсвэл гарах", UninstallDefaultAction: "устгах", UninstallCancelled: "устгалыг хэрэглэгч цуцалсан", ServiceNameLabel: "Үйлчилгээний нэр", PortLabel: "Портууд", StoragePathLabel: "Өгөгдлийн хавтас", DBPathLabel: "Өгөгдлийн сангийн зам", BinaryPathLabel: "Програмын файл", WorkingDirLabel: "Ажлын хавтас", CommandLabel: "Команд", ServiceFileLabel: "Үйлчилгээний файл", DiskSpaceLabel: "Дискийн зай", UnknownAction: "Үл мэдэгдэх үйлдэл"},
	"sv": {LanguageName: "Svenska", InfoLabel: "Info:", DetectedLabel: "Upptackt:", ServiceSystemLabel: "tjanstesystem", InstallTitle: "Installera Sitebrush-tjanst", InstallExplanation: "Sitebrush kor i bakgrunden, startar automatiskt efter omstart och startas nu.", InstallReviewTitle: "Granska fore installation", InstallHelp: "Enter fortsatter. Ett nummer andrar en installning. q avslutar.", InstallPrompt: "Fortsatt, andra nummer eller avsluta", InstallDefaultAction: "fortsatt", InstallCancelled: "installationen avbrots av anvandaren", UninstallTitle: "Ta bort Sitebrush-tjanst", UninstallExplanation: "Sitebrush stoppas och tas bort fran automatisk start.", KeepDataExplanation: "Webbplatsfiler, databas och uppladdningar finns kvar pa disken.", UninstallReviewTitle: "Granska fore borttagning", UninstallHelp: "Enter tar bort tjansten. 1 andrar tjanstnamn. q avslutar.", UninstallPrompt: "Ta bort tjanst, andra nummer eller avsluta", UninstallDefaultAction: "ta bort", UninstallCancelled: "borttagningen avbrots av anvandaren", ServiceNameLabel: "Tjanstnamn", PortLabel: "Portar", StoragePathLabel: "Datamapp", DBPathLabel: "Databasvag", BinaryPathLabel: "Programfil", WorkingDirLabel: "Arbetsmapp", CommandLabel: "Kommando", ServiceFileLabel: "Tjanstefil", DiskSpaceLabel: "Diskutrymme", UnknownAction: "Okand atgard"},
}

func Install(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	probe := newRuntimeProbe()
	manager, err := detectServiceManager(probe)
	if err != nil {
		return Result{}, err
	}
	if options.Input != nil {
		options, err = runInteractiveWizard(ctx, options, probe, manager)
		if err != nil {
			return Result{}, err
		}
	}
	plan, err := buildInstallPlan(options)
	if err != nil {
		return Result{}, err
	}
	if err := prepareInstallFilesystem(plan); err != nil {
		return Result{}, err
	}
	result, err := manager.Install(ctx, probe, plan)
	if err != nil {
		return Result{}, err
	}
	result.OS = probe.goos
	result.Arch = probe.goarch
	result.OSVersion = probe.osVersion
	result.InitSystem = manager.Name
	result.BinaryPath = plan.BinaryPath
	if err := writeServiceMetadata(manager.Name, plan, options.Language); err != nil {
		return Result{}, err
	}
	printResult(options.Output, result, options.Language)
	return result, nil
}

func Uninstall(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	probe := newRuntimeProbe()
	manager, err := detectServiceManager(probe)
	if err != nil {
		return Result{}, err
	}
	options = applyStoredServiceMetadata(options, manager.Name)
	if options.Input != nil {
		options, err = runInteractiveUninstallWizard(ctx, options, probe, manager)
		if err != nil {
			return Result{}, err
		}
	}
	plan, err := buildInstallPlan(options)
	if err != nil {
		return Result{}, err
	}
	result, err := manager.Uninstall(ctx, probe, plan)
	if err != nil {
		return Result{}, err
	}
	result.OS = probe.goos
	result.Arch = probe.goarch
	result.OSVersion = probe.osVersion
	result.InitSystem = manager.Name
	result.BinaryPath = plan.BinaryPath
	if err := removeServiceMetadata(manager.Name, plan.ServiceName); err != nil {
		return Result{}, err
	}
	printUninstallResult(options.Output, result, options.Language)
	return result, nil
}

func newRuntimeProbe() runtimeProbe {
	return runtimeProbe{
		goos:      runtime.GOOS,
		goarch:    runtime.GOARCH,
		osVersion: detectOSVersion(),
		lookPath:  exec.LookPath,
		fileExists: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && !info.IsDir()
		},
		dirExists: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && info.IsDir()
		},
		commandRuns: runCommand,
	}
}

func detectOSVersion() string {
	for _, path := range []string{"/etc/os-release", "/etc/openwrt_release"} {
		content, err := os.ReadFile(path)
		if err == nil {
			return firstUsefulVersionLine(string(content))
		}
	}
	if runtime.GOOS == "darwin" {
		out, err := runCommand(context.Background(), "sw_vers", "-productVersion")
		if err == nil {
			return strings.TrimSpace(out)
		}
	}
	if runtime.GOOS == "windows" {
		out, err := runCommand(context.Background(), "cmd", "/c", "ver")
		if err == nil {
			return strings.TrimSpace(out)
		}
	}
	out, err := runCommand(context.Background(), "uname", "-r")
	if err == nil {
		return strings.TrimSpace(out)
	}
	return "unknown"
}

func firstUsefulVersionLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") || strings.HasPrefix(line, "DISTRIB_DESCRIPTION=") {
			return strings.Trim(strings.TrimPrefix(strings.TrimPrefix(line, "PRETTY_NAME="), "DISTRIB_DESCRIPTION="), `"`)
		}
	}
	return "unknown"
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, name, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func detectServiceManager(probe runtimeProbe) (serviceManager, error) {
	managers := serviceManagersForOS(probe.goos)
	for _, manager := range managers {
		if manager.Detect(probe) {
			return manager, nil
		}
	}
	return serviceManager{}, fmt.Errorf("no supported service/init system detected for %s/%s; supported systems are Linux systemd, SysV init, OpenRC, runit, Upstart; macOS launchd; Windows Service; FreeBSD rc.d; OpenBSD rc.d/rcctl; NetBSD rc.d", probe.goos, probe.goarch)
}

func promptLanguage(ctx context.Context, reader *bufio.Reader, out io.Writer, theme cliTheme) (string, error) {
	return promptLanguageWithDefault(ctx, reader, out, theme, "en")
}

func promptLanguageWithDefault(ctx context.Context, reader *bufio.Reader, out io.Writer, theme cliTheme, defaultLanguage string) (string, error) {
	defaultLanguage = normalizeLanguageCode(defaultLanguage)
	if defaultLanguage == "" {
		defaultLanguage = "en"
	}
	text := cliTextForLanguage(defaultLanguage)
	fmt.Fprintf(out, "\n%s\n", theme.accent(text.LanguageTitle))
	for index, languageCode := range cliLanguageOrder {
		languageText := cliTextForLanguage(languageCode)
		fmt.Fprintf(out, "  [%d] %s (%s)\n", index+1, languageText.LanguageName, languageCode)
	}
	answer, err := promptLine(ctx, reader, out, text.LanguagePrompt, defaultLanguage)
	if err != nil {
		return "", err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if selectedIndex, err := strconv.Atoi(answer); err == nil {
		if selectedIndex >= 1 && selectedIndex <= len(cliLanguageOrder) {
			return cliLanguageOrder[selectedIndex-1], nil
		}
	}
	if normalized := normalizeLanguageCode(answer); normalized != "" {
		return normalized, nil
	}
	return defaultLanguage, nil
}

func normalizeLanguageCode(languageCode string) string {
	languageCode = strings.ToLower(strings.TrimSpace(languageCode))
	if languageCode == "" {
		return ""
	}
	languageCode = strings.ReplaceAll(languageCode, "_", "-")
	if dash := strings.Index(languageCode, "-"); dash >= 0 {
		languageCode = languageCode[:dash]
	}
	if _, ok := cliTranslations[languageCode]; ok {
		return languageCode
	}
	return ""
}

func cliTextForLanguage(languageCode string) cliText {
	languageCode = normalizeLanguageCode(languageCode)
	if languageCode == "" {
		languageCode = "en"
	}
	text, ok := cliTranslations[languageCode]
	if ok {
		text = applyLocalizedCLITextOverrides(languageCode, text)
		return fillCLITextDefaults(text)
	}
	return cliTranslations["en"]
}

func applyLocalizedCLITextOverrides(languageCode string, text cliText) cliText {
	switch languageCode {
	case "de":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "Sprache der Oberflache auswahlen", "Sprachnummer oder Code", "Betriebssystem"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "Sichere Vorgabe: Enter lasst den Dienst installiert. Mit Links/Rechts auswahlen, dann Enter drucken. 1 andert den Dienstnamen. q beendet.", "Dienst behalten, entfernen schreiben, Nummer andern oder beenden", "behalten"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "Dienst behalten", "Dienst entfernen", "Links/Rechts wahlt. Enter bestatigt. 1 andert den Dienstnamen. q beendet."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush wurde als Dienst installiert", "Sitebrush-Dienst wurde gestoppt und aus dem Autostart entfernt", "Befehle", "Installierte Programmdatei bleibt erhalten"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "frei von", "unbekannt"
	case "es":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "Elija el idioma de la interfaz", "Numero o codigo de idioma", "Sistema operativo"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "Valor seguro: Enter mantiene el servicio instalado. Use Izquierda/Derecha para elegir y pulse Enter. Escriba 1 para cambiar el nombre. Escriba q para salir.", "Mantener servicio, escribir eliminar, cambiar numero o salir", "mantener"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "Mantener servicio", "Eliminar servicio", "Izquierda/Derecha elige. Enter confirma. 1 cambia el nombre. q sale."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush instalado como servicio", "Servicio Sitebrush detenido y eliminado del inicio automatico", "Comandos", "El archivo del programa instalado se conserva"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "libres de", "desconocido"
	case "fr":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "Choisir la langue de l'interface", "Numero ou code de langue", "Systeme"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "Choix sur: Entree garde le service installe. Utilisez Gauche/Droite puis Entree. Tapez 1 pour changer le nom. Tapez q pour quitter.", "Garder le service, ecrire supprimer, modifier un numero ou quitter", "garder"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "Garder le service", "Supprimer le service", "Gauche/Droite selectionne. Entree confirme. 1 modifie le nom. q quitte."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush installe comme service", "Service Sitebrush arrete et retire du demarrage automatique", "Commandes", "Le fichier programme installe est conserve"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "libres sur", "inconnu"
	case "it":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "Scegli la lingua dell'interfaccia", "Numero o codice lingua", "Sistema operativo"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "Predefinito sicuro: Invio mantiene il servizio installato. Usa Sinistra/Destra e poi Invio. Scrivi 1 per cambiare nome. Scrivi q per uscire.", "Mantieni servizio, scrivi rimuovi, cambia numero o esci", "mantieni"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "Mantieni servizio", "Rimuovi servizio", "Sinistra/Destra seleziona. Invio conferma. 1 cambia nome. q esce."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush installato come servizio", "Servizio Sitebrush fermato e rimosso dall'avvio automatico", "Comandi", "Il file programma installato resta al suo posto"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "liberi su", "sconosciuto"
	case "pt":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "Escolha o idioma da interface", "Numero ou codigo do idioma", "Sistema operacional"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "Padrao seguro: Enter mantem o servico instalado. Use Esquerda/Direita e pressione Enter. Digite 1 para alterar o nome. Digite q para sair.", "Manter servico, escrever remover, alterar numero ou sair", "manter"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "Manter servico", "Remover servico", "Esquerda/Direita seleciona. Enter confirma. 1 altera o nome. q sai."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush instalado como servico", "Servico Sitebrush parado e removido da inicializacao automatica", "Comandos", "Arquivo do programa instalado mantido no lugar"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "livres de", "desconhecido"
	case "tr":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "Arayuz dilini secin", "Dil numarasi veya kodu", "Isletim sistemi"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "Guvenli varsayilan: Enter servisi kurulu tutar. Sol/Sag ile secip Enter'a basin. Adi degistirmek icin 1. Cikmak icin q.", "Servisi tut, kaldir yaz, numara degistir veya cik", "tut"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "Servisi tut", "Servisi kaldir", "Sol/Sag secer. Enter onaylar. 1 adi degistirir. q cikar."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush servis olarak kuruldu", "Sitebrush servisi durduruldu ve otomatik baslangictan kaldirildi", "Komutlar", "Kurulu program dosyasi yerinde birakildi"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "bos / toplam", "bilinmiyor"
	case "zh":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "选择界面语言", "语言编号或代码", "操作系统"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "安全默认值：按 Enter 会保留服务。用左/右方向键选择，然后按 Enter。输入 1 修改服务名，输入 q 退出。", "保留服务、输入删除、修改编号或退出", "保留"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "保留服务", "删除服务", "左/右选择。Enter 确认。1 修改服务名。q 退出。"
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush 已安装为服务", "Sitebrush 服务已停止并从自动启动中移除", "命令", "已安装的程序文件保留在原位置"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "可用，共", "未知"
	case "ja":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "インターフェイス言語を選択", "言語番号またはコード", "OS"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "安全な既定値: Enter ではサービスを残します。左右キーで選択して Enter。名前変更は 1、終了は q。", "サービスを残す、削除と入力、番号変更、または終了", "残す"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "サービスを残す", "サービスを削除", "左右で選択。Enter で確定。1 で名前変更。q で終了。"
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush をサービスとしてインストールしました", "Sitebrush サービスを停止し自動起動から削除しました", "コマンド", "インストール済みプログラムファイルは残します"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "空き / 合計", "不明"
	case "fa":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "زبان رابط را انتخاب کنید", "شماره یا کد زبان", "سیستم عامل"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "پیش فرض امن: Enter سرویس را نگه می دارد. با چپ/راست انتخاب کنید و Enter بزنید. برای تغییر نام 1 و برای خروج q.", "نگه داشتن سرویس، نوشتن حذف، تغییر شماره یا خروج", "نگه داشتن"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "نگه داشتن سرویس", "حذف سرویس", "چپ/راست انتخاب می کند. Enter تایید می کند. 1 نام را تغییر می دهد. q خارج می شود."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush به عنوان سرویس نصب شد", "سرویس Sitebrush متوقف و از شروع خودکار حذف شد", "دستورها", "فایل برنامه نصب شده در جای خود باقی ماند"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "آزاد از", "نامشخص"
	case "he":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "בחר שפת ממשק", "מספר או קוד שפה", "מערכת הפעלה"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "ברירת מחדל בטוחה: Enter משאיר את השירות מותקן. בחר עם שמאלה/ימינה ואז Enter. 1 משנה שם, q יוצא.", "השאר שירות, כתוב הסר, שנה מספר או צא", "השאר"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "השאר שירות", "הסר שירות", "שמאלה/ימינה בוחרים. Enter מאשר. 1 משנה שם. q יוצא."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush הותקן כשירות", "שירות Sitebrush נעצר והוסר מהפעלה אוטומטית", "פקודות", "קובץ התוכנה המותקן נשאר במקומו"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "פנוי מתוך", "לא ידוע"
	case "fi":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "Valitse kayttoliittyman kieli", "Kielen numero tai koodi", "Kayttojarjestelma"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "Turvallinen oletus: Enter jattaa palvelun asennetuksi. Valitse Vasen/Oikea ja paina Enter. 1 muuttaa nimen. q poistuu.", "Sailyta palvelu, kirjoita poista, muuta numeroa tai poistu", "sailyta"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "Sailyta palvelu", "Poista palvelu", "Vasen/Oikea valitsee. Enter vahvistaa. 1 muuttaa nimen. q poistuu."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush asennettu palveluksi", "Sitebrush-palvelu pysaytetty ja poistettu automaattisesta kaynnistyksesta", "Komennot", "Asennettu ohjelmatiedosto jatettiin paikalleen"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "vapaana / yhteensa", "tuntematon"
	case "kk":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "Интерфейс тілін таңдаңыз", "Тіл нөмірі немесе коды", "ОЖ"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "Қауіпсіз әдепкі мән: Enter қызметті қалдырады. Сол/оң жақпен таңдап, Enter басыңыз. Атын өзгерту үшін 1. Шығу үшін q.", "Қызметті қалдыру, жою деп жазу, нөмірді өзгерту немесе шығу", "қалдыру"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "Қызметті қалдыру", "Қызметті жою", "Сол/оң таңдайды. Enter растайды. 1 атын өзгертеді. q шығады."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush қызмет ретінде орнатылды", "Sitebrush қызметі тоқтатылып, автожүктеуден алынды", "Командалар", "Орнатылған бағдарлама файлы орнында қалды"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "бос / жалпы", "белгісіз"
	case "mn":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "Интерфейсийн хэл сонгоно уу", "Хэлний дугаар эсвэл код", "ҮС"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "Аюулгүй өгөгдмөл: Enter үйлчилгээ хэвээр үлдээнэ. Зүүн/баруун сум сонгоод Enter дарна. Нэр солих бол 1. Гарах бол q.", "Үйлчилгээг үлдээх, устгах гэж бичих, дугаар өөрчлөх эсвэл гарах", "үлдээх"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "Үйлчилгээг үлдээх", "Үйлчилгээг устгах", "Зүүн/баруун сонгоно. Enter батална. 1 нэр өөрчилнө. q гарна."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush үйлчилгээ болж суусан", "Sitebrush үйлчилгээ зогсож, автоматаар эхлэхээс хасагдсан", "Командууд", "Суулгасан програмын файл байрандаа үлдсэн"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "сул / нийт", "тодорхойгүй"
	case "sv":
		text.LanguageTitle, text.LanguagePrompt, text.OSLabel = "Valj granssonittets sprak", "Spraknummer eller kod", "Operativsystem"
		text.UninstallHelp, text.UninstallPrompt, text.UninstallDefaultAction = "Sakert standardval: Enter behaller tjansten installerad. Valj med Vanster/Hoger och tryck Enter. 1 andrar namn. q avslutar.", "Behall tjanst, skriv ta bort, andra nummer eller avsluta", "behall"
		text.UninstallKeepOption, text.UninstallRemoveOption, text.UninstallSelectHelp = "Behall tjanst", "Ta bort tjanst", "Vanster/Hoger valjer. Enter bekraftar. 1 andrar namn. q avslutar."
		text.InstallComplete, text.UninstallComplete, text.CommandsLabel, text.BinaryLeftLabel = "Sitebrush installerades som tjanst", "Sitebrush-tjansten stoppades och togs bort fran autostart", "Kommandon", "Installerad programfil lamnades kvar"
		text.DiskSpaceFreeOf, text.DiskSpaceUnknown = "ledigt av", "okant"
	}
	return text
}

func fillCLITextDefaults(text cliText) cliText {
	defaultText := cliTranslations["en"]
	if text.LanguageTitle == "" {
		text.LanguageTitle = defaultText.LanguageTitle
	}
	if text.LanguagePrompt == "" {
		text.LanguagePrompt = defaultText.LanguagePrompt
	}
	if text.OSLabel == "" {
		text.OSLabel = defaultText.OSLabel
	}
	if text.InstallComplete == "" {
		text.InstallComplete = defaultText.InstallComplete
	}
	if text.UninstallComplete == "" {
		text.UninstallComplete = defaultText.UninstallComplete
	}
	if text.CommandsLabel == "" {
		text.CommandsLabel = defaultText.CommandsLabel
	}
	if text.BinaryLeftLabel == "" {
		text.BinaryLeftLabel = defaultText.BinaryLeftLabel
	}
	if text.UninstallKeepOption == "" {
		text.UninstallHelp = defaultText.UninstallHelp
		text.UninstallPrompt = defaultText.UninstallPrompt
		text.UninstallDefaultAction = defaultText.UninstallDefaultAction
		text.UninstallKeepOption = defaultText.UninstallKeepOption
		text.UninstallRemoveOption = defaultText.UninstallRemoveOption
		text.UninstallSelectHelp = defaultText.UninstallSelectHelp
	}
	if text.UninstallRemoveOption == "" {
		text.UninstallRemoveOption = defaultText.UninstallRemoveOption
	}
	if text.UninstallSelectHelp == "" {
		text.UninstallSelectHelp = defaultText.UninstallSelectHelp
	}
	if text.DiskSpaceFreeOf == "" {
		text.DiskSpaceFreeOf = defaultText.DiskSpaceFreeOf
	}
	if text.DiskSpaceUnknown == "" {
		text.DiskSpaceUnknown = defaultText.DiskSpaceUnknown
	}
	return text
}

func runInteractiveWizard(ctx context.Context, options Options, probe runtimeProbe, manager serviceManager) (Options, error) {
	out := options.Output
	if out == nil {
		out = io.Discard
	}
	theme := resolveCLITheme(out)
	reader := bufio.NewReader(options.Input)
	options.Language = normalizeLanguageCode(options.Language)
	if options.Language == "" {
		language, err := promptLanguage(ctx, reader, out, theme)
		if err != nil {
			return Options{}, err
		}
		options.Language = language
	}
	text := cliTextForLanguage(options.Language)
	fmt.Fprintf(out, "\n%s\n", theme.accent(text.InstallTitle))
	fmt.Fprintf(out, "%s %s\n", theme.dim(text.InfoLabel), text.InstallExplanation)
	fmt.Fprintf(out, "%s %s %s (%s), %s: %s\n", theme.dim(text.DetectedLabel), probe.goos, probe.osVersion, probe.goarch, text.ServiceSystemLabel, theme.accent(manager.Name))
	for {
		plan, err := buildInstallPlan(options)
		if err != nil {
			return Options{}, err
		}
		storagePath := installOptionOrDefault(options.StoragePath, defaultStoragePath())
		fmt.Fprintf(out, "\n%s\n", theme.accent(text.InstallReviewTitle))
		fmt.Fprintf(out, "  [1] %s: %s\n", text.ServiceNameLabel, plan.ServiceName)
		fmt.Fprintf(out, "  [2] %s: %s\n", text.PortLabel, installOptionOrDefault(options.Port, "80,443"))
		fmt.Fprintf(out, "  [3] %s: %s\n", text.StoragePathLabel, storagePath)
		fmt.Fprintf(out, "      %s: %s\n", text.DiskSpaceLabel, diskSpaceSummary(storagePath, text))
		fmt.Fprintf(out, "  [4] %s: %s\n", text.DBPathLabel, installOptionOrDefault(options.DBPath, filepath.Join(storagePath, "storage", "db", "sitebrush.db")))
		fmt.Fprintf(out, "  [5] %s: %s\n", text.BinaryPathLabel, plan.BinaryPath)
		fmt.Fprintf(out, "  [6] %s: %s\n", text.WorkingDirLabel, plan.WorkingDir)
		fmt.Fprintf(out, "      %s: %s\n", text.CommandLabel, strings.Join(plan.ExecArgs, " "))
		fmt.Fprintf(out, "\n%s\n", theme.dim(text.InstallHelp))
		action, err := promptLine(ctx, reader, out, text.InstallPrompt, text.InstallDefaultAction)
		if err != nil {
			return Options{}, err
		}
		actionKey := normalizeCLIAction(action)
		switch {
		case actionMatches(actionKey, "", text.InstallDefaultAction, "apply", "a", "continue", "c", "yes", "y", "да", "д", "применить", "продолжить", "установить"):
			return options, nil
		case actionMatches(actionKey, "quit", "q", "exit", "cancel", "отмена", "выйти", "выход"):
			return Options{}, fmt.Errorf("%s: %w", text.InstallCancelled, ErrCancelled)
		case actionKey == "1":
			value, err := promptLine(ctx, reader, out, text.ServiceNameLabel, installOptionOrDefault(options.ServiceName, "sitebrush"))
			if err != nil {
				return Options{}, err
			}
			options.ServiceName = value
		case actionKey == "2":
			value, err := promptLine(ctx, reader, out, text.PortLabel, installOptionOrDefault(options.Port, "80,443"))
			if err != nil {
				return Options{}, err
			}
			options.Port = value
		case actionKey == "3":
			value, err := promptLine(ctx, reader, out, text.StoragePathLabel, storagePath)
			if err != nil {
				return Options{}, err
			}
			options.StoragePath = value
			if strings.TrimSpace(options.DBPath) == "" {
				options.DBPath = filepath.Join(value, "storage", "db", "sitebrush.db")
			}
			if strings.TrimSpace(options.WorkingDir) == "" {
				options.WorkingDir = value
			}
		case actionKey == "4":
			defaultDBPath := filepath.Join(installOptionOrDefault(options.StoragePath, defaultStoragePath()), "storage", "db", "sitebrush.db")
			value, err := promptLine(ctx, reader, out, text.DBPathLabel, installOptionOrDefault(options.DBPath, defaultDBPath))
			if err != nil {
				return Options{}, err
			}
			options.DBPath = value
		case actionKey == "5":
			value, err := promptLine(ctx, reader, out, text.BinaryPathLabel, installOptionOrDefault(options.BinaryPath, os.Args[0]))
			if err != nil {
				return Options{}, err
			}
			options.BinaryPath = value
		case actionKey == "6":
			value, err := promptLine(ctx, reader, out, text.WorkingDirLabel, installOptionOrDefault(options.WorkingDir, installOptionOrDefault(options.StoragePath, defaultStoragePath())))
			if err != nil {
				return Options{}, err
			}
			options.WorkingDir = value
		default:
			fmt.Fprintf(out, "%s %q\n", text.UnknownAction, action)
		}
	}
}

func runInteractiveUninstallWizard(ctx context.Context, options Options, probe runtimeProbe, manager serviceManager) (Options, error) {
	out := options.Output
	if out == nil {
		out = io.Discard
	}
	theme := resolveCLITheme(out)
	reader := bufio.NewReader(options.Input)
	options.Language = normalizeLanguageCode(options.Language)
	defaultLanguage := options.Language
	if defaultLanguage == "" {
		defaultLanguage = "en"
	}
	language, err := promptLanguageWithDefault(ctx, reader, out, theme, defaultLanguage)
	if err != nil {
		return Options{}, err
	}
	options.Language = language
	text := cliTextForLanguage(options.Language)
	fmt.Fprintf(out, "\n%s\n", theme.warn(text.UninstallTitle))
	fmt.Fprintf(out, "%s %s\n", theme.dim(text.InfoLabel), text.UninstallExplanation)
	fmt.Fprintf(out, "%s %s\n", theme.dim(text.InfoLabel), text.KeepDataExplanation)
	fmt.Fprintf(out, "%s %s %s (%s), %s: %s\n", theme.dim(text.DetectedLabel), probe.goos, probe.osVersion, probe.goarch, text.ServiceSystemLabel, theme.accent(manager.Name))
	for {
		plan, err := buildInstallPlan(options)
		if err != nil {
			return Options{}, err
		}
		storagePath := installOptionOrDefault(options.StoragePath, defaultStoragePath())
		fmt.Fprintf(out, "\n%s\n", theme.warn(text.UninstallReviewTitle))
		fmt.Fprintf(out, "  [1] %s: %s\n", text.ServiceNameLabel, plan.ServiceName)
		fmt.Fprintf(out, "      %s: %s\n", text.ServiceFileLabel, expectedServicePath(manager.Name, plan.ServiceName))
		fmt.Fprintf(out, "      %s: %s\n", text.BinaryPathLabel, plan.BinaryPath)
		fmt.Fprintf(out, "      %s: %s\n", text.StoragePathLabel, storagePath)
		fmt.Fprintf(out, "      %s: %s\n", text.DiskSpaceLabel, diskSpaceSummary(storagePath, text))
		fmt.Fprintf(out, "\n%s\n", theme.dim(text.UninstallHelp))
		action, err := promptUninstallAction(ctx, options.Input, reader, out, text, theme)
		if err != nil {
			return Options{}, err
		}
		actionKey := normalizeCLIAction(action)
		switch {
		case actionMatches(actionKey, "remove", "delete", "uninstall", "u", "apply", "a", "yes", "y", "да", "д", "удалить"):
			return options, nil
		case actionMatches(actionKey, "", text.UninstallDefaultAction, "keep", "safe", "no", "n", "quit", "q", "exit", "cancel", "отмена", "выйти", "выход", "оставить", "нет"):
			return Options{}, fmt.Errorf("%s: %w", text.UninstallCancelled, ErrCancelled)
		case actionKey == "1":
			value, err := promptLine(ctx, reader, out, text.ServiceNameLabel, installOptionOrDefault(options.ServiceName, "sitebrush"))
			if err != nil {
				return Options{}, err
			}
			options.ServiceName = value
		default:
			fmt.Fprintf(out, "%s %q\n", text.UnknownAction, action)
		}
	}
}

func resolveCLITheme(out io.Writer) cliTheme {
	if os.Getenv("NO_COLOR") != "" {
		return cliTheme{}
	}
	file, ok := out.(*os.File)
	if !ok {
		return cliTheme{}
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return cliTheme{}
	}
	return cliTheme{enabled: true}
}

func (theme cliTheme) color(code, value string) string {
	if !theme.enabled {
		return value
	}
	return "\033[" + code + "m" + value + "\033[0m"
}

func (theme cliTheme) accent(value string) string {
	return theme.color("36;1", value)
}

func (theme cliTheme) warn(value string) string {
	return theme.color("33;1", value)
}

func (theme cliTheme) success(value string) string {
	return theme.color("32;1", value)
}

func (theme cliTheme) prompt(value string) string {
	return theme.color("35;1", value)
}

func (theme cliTheme) dim(value string) string {
	return theme.color("2", value)
}

func expectedServicePath(managerName, serviceName string) string {
	switch managerName {
	case "systemd":
		return filepath.Join("/etc/systemd/system", serviceName+".service")
	case "OpenRC", "SysV init":
		return filepath.Join("/etc/init.d", serviceName)
	case "runit":
		return filepath.Join("/etc/sv", serviceName, "run")
	case "Upstart":
		return filepath.Join("/etc/init", serviceName+".conf")
	case "launchd":
		return filepath.Join("/Library/LaunchDaemons", "net.sitebrush."+serviceName+".plist")
	case "Windows Service":
		return "Windows Service: " + serviceName
	case "rc.d":
		if runtime.GOOS == "freebsd" {
			return filepath.Join("/usr/local/etc/rc.d", serviceName)
		}
		return filepath.Join("/etc/rc.d", serviceName)
	case "rc.d/rcctl":
		return filepath.Join("/etc/rc.d", serviceName)
	default:
		return serviceName
	}
}

func expectedServiceMetadataPath(managerName, serviceName string) string {
	return expectedServicePath(managerName, serviceName) + ".sitebrush.json"
}

func writeServiceMetadata(managerName string, plan installPlan, languageCode string) error {
	metadata := serviceMetadata{
		Language:    normalizeLanguageCode(languageCode),
		ServiceName: plan.ServiceName,
		Port:        installOptionOrDefault(plan.Options.Port, "80,443"),
		StoragePath: installOptionOrDefault(plan.Options.StoragePath, defaultStoragePath()),
		DBType:      installOptionOrDefault(plan.Options.DBType, "sqlite"),
		DBPath:      installOptionOrDefault(plan.Options.DBPath, filepath.Join(installOptionOrDefault(plan.Options.StoragePath, defaultStoragePath()), "storage", "db", "sitebrush.db")),
		BinaryPath:  plan.BinaryPath,
		WorkingDir:  plan.WorkingDir,
	}
	if metadata.Language == "" {
		metadata.Language = "en"
	}
	content, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode service metadata: %w", err)
	}
	return writeFile(expectedServiceMetadataPath(managerName, plan.ServiceName), string(content)+"\n", 0o644)
}

func readServiceMetadata(managerName, serviceName string) (serviceMetadata, bool) {
	content, err := os.ReadFile(expectedServiceMetadataPath(managerName, serviceName))
	if err != nil {
		return serviceMetadata{}, false
	}
	var metadata serviceMetadata
	if err := json.Unmarshal(content, &metadata); err != nil {
		return serviceMetadata{}, false
	}
	return metadata, true
}

func applyStoredServiceMetadata(options Options, managerName string) Options {
	serviceName := installOptionOrDefault(options.ServiceName, "sitebrush")
	metadata, ok := readServiceMetadata(managerName, serviceName)
	if !ok {
		return options
	}
	if strings.TrimSpace(options.Language) == "" {
		options.Language = metadata.Language
	}
	options.Port = installOptionOrDefault(metadata.Port, options.Port)
	options.StoragePath = installOptionOrDefault(metadata.StoragePath, options.StoragePath)
	options.DBType = installOptionOrDefault(metadata.DBType, options.DBType)
	options.DBPath = installOptionOrDefault(metadata.DBPath, options.DBPath)
	options.WorkingDir = installOptionOrDefault(metadata.WorkingDir, options.WorkingDir)
	return options
}

func removeServiceMetadata(managerName, serviceName string) error {
	return removeFileIfExists(expectedServiceMetadataPath(managerName, serviceName))
}

func installOptionOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func normalizeCLIAction(action string) string {
	return strings.ToLower(strings.TrimSpace(action))
}

func actionMatches(action string, candidates ...string) bool {
	action = normalizeCLIAction(action)
	for _, candidate := range candidates {
		if action == normalizeCLIAction(candidate) {
			return true
		}
	}
	return false
}

func promptUninstallAction(ctx context.Context, input io.Reader, reader *bufio.Reader, out io.Writer, text cliText, theme cliTheme) (string, error) {
	inputFile, inputIsFile := input.(*os.File)
	outputFile, outputIsFile := out.(*os.File)
	if inputIsFile && outputIsFile && term.IsTerminal(int(inputFile.Fd())) && term.IsTerminal(int(outputFile.Fd())) {
		return promptUninstallActionWithArrows(inputFile, out, text, theme)
	}
	return promptLine(ctx, reader, out, text.UninstallPrompt, text.UninstallDefaultAction)
}

func promptUninstallActionWithArrows(input *os.File, out io.Writer, text cliText, theme cliTheme) (string, error) {
	oldState, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(input.Fd()), oldState)
	selectedRemove := false
	drawUninstallChoice(out, text, theme, selectedRemove)
	for {
		key, err := readTerminalKey(input)
		if err != nil {
			return "", err
		}
		switch key {
		case "left":
			selectedRemove = false
			drawUninstallChoice(out, text, theme, selectedRemove)
		case "right":
			selectedRemove = true
			drawUninstallChoice(out, text, theme, selectedRemove)
		case "tab", "space":
			selectedRemove = !selectedRemove
			drawUninstallChoice(out, text, theme, selectedRemove)
		case "enter":
			fmt.Fprint(out, "\r\n")
			if selectedRemove {
				return "remove", nil
			}
			return "keep", nil
		case "q", "esc":
			fmt.Fprint(out, "\r\n")
			return "keep", nil
		case "1":
			fmt.Fprint(out, "\r\n")
			return "1", nil
		}
	}
}

func drawUninstallChoice(out io.Writer, text cliText, theme cliTheme, selectedRemove bool) {
	keep := text.UninstallKeepOption
	remove := text.UninstallRemoveOption
	if selectedRemove {
		keep = theme.dim("  " + keep + "  ")
		remove = theme.warn("> " + remove + " <")
	} else {
		keep = theme.success("> " + keep + " <")
		remove = theme.dim("  " + remove + "  ")
	}
	fmt.Fprintf(out, "\r\033[2K%s  %s  %s", theme.dim(text.UninstallSelectHelp), keep, remove)
}

func readTerminalKey(input *os.File) (string, error) {
	var buffer [1]byte
	for {
		n, err := input.Read(buffer[:])
		if err != nil {
			return "", err
		}
		if n == 0 {
			continue
		}
		switch buffer[0] {
		case '\r', '\n':
			return "enter", nil
		case '\t':
			return "tab", nil
		case ' ':
			return "space", nil
		case 'q', 'Q':
			return "q", nil
		case '1':
			return "1", nil
		case 0x1b:
			return readEscapeKey(input)
		}
	}
}

func readEscapeKey(input *os.File) (string, error) {
	var sequence [2]byte
	if err := input.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err == nil {
		defer input.SetReadDeadline(time.Time{})
	}
	n, err := input.Read(sequence[:])
	if err != nil || n == 0 {
		return "esc", nil
	}
	if n == 2 && sequence[0] == '[' {
		switch sequence[1] {
		case 'D':
			return "left", nil
		case 'C':
			return "right", nil
		}
	}
	return "esc", nil
}

func diskSpaceSummary(path string, text cliText) string {
	freeBytes, totalBytes, ok := diskSpace(path)
	if !ok {
		return text.DiskSpaceUnknown
	}
	return fmt.Sprintf("%s %s %s", formatBytes(freeBytes), text.DiskSpaceFreeOf, formatBytes(totalBytes))
}

func formatBytes(size uint64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	for _, suffix := range []string{"KB", "MB", "GB", "TB", "PB"} {
		value = value / unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f EB", value/unit)
}

func promptLine(ctx context.Context, reader *bufio.Reader, out io.Writer, label, fallback string) (string, error) {
	theme := resolveCLITheme(out)
	fmt.Fprintf(out, "%s [%s]: ", theme.prompt(label), theme.accent(fallback))
	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := reader.ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		lineCh <- line
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-errCh:
		if errors.Is(err, io.EOF) {
			return fallback, nil
		}
		return "", err
	case line := <-lineCh:
		cleaned := strings.TrimSpace(line)
		if cleaned == "" {
			return fallback, nil
		}
		return cleaned, nil
	}
}

func serviceManagersForOS(goos string) []serviceManager {
	switch goos {
	case "linux":
		return []serviceManager{
			{Name: "systemd", Priority: 10, Detect: detectSystemd, Install: installSystemd, Uninstall: uninstallSystemd},
			{Name: "OpenRC", Priority: 20, Detect: detectOpenRC, Install: installOpenRC, Uninstall: uninstallOpenRC},
			{Name: "runit", Priority: 30, Detect: detectRunit, Install: installRunit, Uninstall: uninstallRunit},
			{Name: "Upstart", Priority: 40, Detect: detectUpstart, Install: installUpstart, Uninstall: uninstallUpstart},
			{Name: "SysV init", Priority: 50, Detect: detectSysVInit, Install: installSysVInit, Uninstall: uninstallSysVInit},
		}
	case "darwin":
		return []serviceManager{{Name: "launchd", Detect: detectLaunchd, Install: installLaunchd, Uninstall: uninstallLaunchd}}
	case "windows":
		return []serviceManager{{Name: "Windows Service", Detect: detectWindowsService, Install: installWindowsService, Uninstall: uninstallWindowsService}}
	case "freebsd":
		return []serviceManager{{Name: "rc.d", Detect: detectFreeBSDRcD, Install: installFreeBSDRcD, Uninstall: uninstallFreeBSDRcD}}
	case "openbsd":
		return []serviceManager{{Name: "rc.d/rcctl", Detect: detectOpenBSDRcD, Install: installOpenBSDRcD, Uninstall: uninstallOpenBSDRcD}}
	case "netbsd":
		return []serviceManager{{Name: "rc.d", Detect: detectNetBSDRcD, Install: installNetBSDRcD, Uninstall: uninstallNetBSDRcD}}
	default:
		return nil
	}
}

func commandExists(probe runtimeProbe, name string) bool {
	_, err := probe.lookPath(name)
	return err == nil
}

func commandWorks(probe runtimeProbe, name string, args ...string) bool {
	if !commandExists(probe, name) {
		return false
	}
	_, err := probe.commandRuns(context.Background(), name, args...)
	return err == nil
}

func detectSystemd(probe runtimeProbe) bool {
	return probe.dirExists("/run/systemd/system") && commandWorks(probe, "systemctl", "--version") && commandWorks(probe, "systemctl", "list-unit-files", "--type=service", "--no-pager")
}

func detectOpenRC(probe runtimeProbe) bool {
	return commandWorks(probe, "rc-service", "--version") && commandWorks(probe, "rc-update", "--version") && (probe.dirExists("/run/openrc") || probe.dirExists("/etc/init.d"))
}

func detectRunit(probe runtimeProbe) bool {
	return commandExists(probe, "sv") && commandExists(probe, "runsvdir") && (probe.dirExists("/etc/sv") || probe.dirExists("/service") || probe.dirExists("/etc/service"))
}

func detectUpstart(probe runtimeProbe) bool {
	return probe.dirExists("/etc/init") && commandWorks(probe, "initctl", "version")
}

func detectSysVInit(probe runtimeProbe) bool {
	return probe.dirExists("/etc/init.d") && commandWorks(probe, "service", "--status-all") && (commandExists(probe, "update-rc.d") || commandExists(probe, "chkconfig") || commandExists(probe, "insserv"))
}

func detectLaunchd(probe runtimeProbe) bool {
	return commandWorks(probe, "launchctl", "print", "system") && probe.dirExists("/Library/LaunchDaemons")
}

func detectWindowsService(probe runtimeProbe) bool {
	return commandWorks(probe, "sc.exe", "query", "eventlog")
}

func detectFreeBSDRcD(probe runtimeProbe) bool {
	return probe.dirExists("/etc/rc.d") && commandWorks(probe, "service", "-e")
}

func detectOpenBSDRcD(probe runtimeProbe) bool {
	return probe.dirExists("/etc/rc.d") && commandWorks(probe, "rcctl", "ls", "all")
}

func detectNetBSDRcD(probe runtimeProbe) bool {
	return probe.dirExists("/etc/rc.d") && (commandWorks(probe, "service", "-e") || probe.fileExists("/etc/rc.subr"))
}

func buildInstallPlan(options Options) (installPlan, error) {
	serviceName := strings.TrimSpace(options.ServiceName)
	if serviceName == "" {
		serviceName = "sitebrush"
	}
	sourceBinary := strings.TrimSpace(options.BinaryPath)
	if sourceBinary == "" {
		exe, err := os.Executable()
		if err != nil {
			return installPlan{}, fmt.Errorf("resolve executable: %w", err)
		}
		sourceBinary = exe
	}
	installedBinary := defaultInstalledBinaryPath(serviceName)
	if sameCleanPath(sourceBinary, installedBinary) {
		installedBinary = sourceBinary
	}
	workingDir := strings.TrimSpace(options.WorkingDir)
	if workingDir == "" {
		workingDir = strings.TrimSpace(options.StoragePath)
	}
	if workingDir == "" {
		workingDir = filepath.Dir(installedBinary)
	}
	port := strings.TrimSpace(options.Port)
	if port == "" {
		port = "80,443"
	}
	storagePath := strings.TrimSpace(options.StoragePath)
	if storagePath == "" {
		storagePath = defaultStoragePath()
	}
	execArgs := []string{installedBinary, "-port", port, "-path", storagePath}
	return installPlan{Options: options, ServiceName: serviceName, BinaryPath: installedBinary, WorkingDir: workingDir, ExecArgs: execArgs}, nil
}

func defaultInstalledBinaryPath(serviceName string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramFiles"), "Sitebrush", serviceName+".exe")
	}
	return filepath.Join("/usr/local/bin", serviceName)
}

func defaultStoragePath() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if strings.TrimSpace(base) == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "Sitebrush")
	}
	return "/var/lib/sitebrush"
}

func sameCleanPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil {
		left = leftAbs
	}
	if rightErr == nil {
		right = rightAbs
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func prepareInstallFilesystem(plan installPlan) error {
	if err := os.MkdirAll(filepath.Dir(plan.BinaryPath), 0o755); err != nil {
		return fmt.Errorf("create binary directory: %w", err)
	}
	if err := os.MkdirAll(plan.WorkingDir, 0o755); err != nil {
		return fmt.Errorf("create working directory: %w", err)
	}
	sourceBinary := strings.TrimSpace(plan.Options.BinaryPath)
	if sourceBinary == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable: %w", err)
		}
		sourceBinary = exe
	}
	if !sameCleanPath(sourceBinary, plan.BinaryPath) {
		if err := copyExecutable(sourceBinary, plan.BinaryPath); err != nil {
			return err
		}
	}
	return nil
}

func copyExecutable(sourcePath, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source binary: %w", err)
	}
	defer source.Close()
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("create installed binary: %w", err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		return fmt.Errorf("copy installed binary: %w", err)
	}
	if err := destination.Close(); err != nil {
		return fmt.Errorf("close installed binary: %w", err)
	}
	return os.Chmod(destinationPath, 0o755)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func execLine(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func windowsCommandLine(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, strconv.Quote(arg))
	}
	return strings.Join(quoted, " ")
}

func writeFile(path, content string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s directory: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func removeFileIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func removeAllIfExists(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func runInstallCommands(ctx context.Context, probe runtimeProbe, commands [][]string) ([]string, error) {
	serviceCommands := make([]serviceCommand, 0, len(commands))
	for _, command := range commands {
		serviceCommands = append(serviceCommands, serviceCommand{Args: command})
	}
	return runServiceCommands(ctx, probe, serviceCommands)
}

func runServiceCommands(ctx context.Context, probe runtimeProbe, commands []serviceCommand) ([]string, error) {
	executed := make([]string, 0, len(commands))
	for _, command := range commands {
		if len(command.Args) == 0 {
			continue
		}
		commandText := strings.Join(command.Args, " ")
		executed = append(executed, commandText)
		if _, err := probe.commandRuns(ctx, command.Args[0], command.Args[1:]...); err != nil && !command.AllowFailure {
			return executed, fmt.Errorf("%s failed: %w", commandText, err)
		}
	}
	return executed, nil
}

func installSystemd(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	unitName := plan.ServiceName + ".service"
	unitPath := filepath.Join("/etc/systemd/system", unitName)
	content := fmt.Sprintf(`[Unit]
Description=Sitebrush server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, plan.WorkingDir, execLine(plan.ExecArgs))
	if err := writeFile(unitPath, content, 0o644); err != nil {
		return Result{}, err
	}
	commands, err := runInstallCommands(ctx, probe, [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "--now", unitName},
		{"systemctl", "is-active", "--quiet", unitName},
	})
	return Result{ServicePath: unitPath, Commands: commands}, err
}

func uninstallSystemd(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	unitName := plan.ServiceName + ".service"
	unitPath := filepath.Join("/etc/systemd/system", unitName)
	commands, err := runServiceCommands(ctx, probe, []serviceCommand{
		{Args: []string{"systemctl", "disable", "--now", unitName}, AllowFailure: true},
		{Args: []string{"systemctl", "daemon-reload"}},
		{Args: []string{"systemctl", "reset-failed", unitName}, AllowFailure: true},
	})
	if removeErr := removeFileIfExists(unitPath); removeErr != nil && err == nil {
		err = removeErr
	}
	if err == nil {
		extraCommands, commandErr := runInstallCommands(ctx, probe, [][]string{{"systemctl", "daemon-reload"}})
		commands = append(commands, extraCommands...)
		err = commandErr
	}
	return Result{ServicePath: unitPath, Commands: commands}, err
}

func installOpenRC(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/etc/init.d", plan.ServiceName)
	content := fmt.Sprintf(`#!/sbin/openrc-run
name="Sitebrush"
description="Sitebrush server"
command=%s
command_args=%s
directory=%s
command_background=true
pidfile="/run/%s.pid"
output_log="/var/log/%s.log"
error_log="/var/log/%s.log"
depend() {
	need net
}
`, shellQuote(plan.BinaryPath), shellQuote(strings.Join(plan.ExecArgs[1:], " ")), shellQuote(plan.WorkingDir), plan.ServiceName, plan.ServiceName, plan.ServiceName)
	if err := writeFile(path, content, 0o755); err != nil {
		return Result{}, err
	}
	commands, err := runInstallCommands(ctx, probe, [][]string{
		{"rc-update", "add", plan.ServiceName, "default"},
		{"rc-service", plan.ServiceName, "restart"},
		{"rc-service", plan.ServiceName, "status"},
	})
	return Result{ServicePath: path, Commands: commands}, err
}

func uninstallOpenRC(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/etc/init.d", plan.ServiceName)
	commands, err := runServiceCommands(ctx, probe, []serviceCommand{
		{Args: []string{"rc-service", plan.ServiceName, "stop"}, AllowFailure: true},
		{Args: []string{"rc-update", "del", plan.ServiceName, "default"}, AllowFailure: true},
	})
	if removeErr := removeFileIfExists(path); removeErr != nil && err == nil {
		err = removeErr
	}
	return Result{ServicePath: path, Commands: commands}, err
}

func installRunit(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	serviceRoot := "/etc/service"
	if !probe.dirExists(serviceRoot) && probe.dirExists("/service") {
		serviceRoot = "/service"
	}
	sourceDir := filepath.Join("/etc/sv", plan.ServiceName)
	runPath := filepath.Join(sourceDir, "run")
	content := fmt.Sprintf(`#!/bin/sh
cd %s || exit 1
exec %s 2>&1
`, shellQuote(plan.WorkingDir), execLine(plan.ExecArgs))
	if err := writeFile(runPath, content, 0o755); err != nil {
		return Result{}, err
	}
	serviceLink := filepath.Join(serviceRoot, plan.ServiceName)
	if _, err := os.Lstat(serviceLink); err != nil {
		if err := os.Symlink(sourceDir, serviceLink); err != nil {
			return Result{}, fmt.Errorf("enable runit service: %w", err)
		}
	}
	commands, err := runInstallCommands(ctx, probe, [][]string{
		{"sv", "up", serviceLink},
		{"sv", "status", serviceLink},
	})
	return Result{ServicePath: runPath, Commands: commands}, err
}

func uninstallRunit(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	serviceRoot := "/etc/service"
	if !probe.dirExists(serviceRoot) && probe.dirExists("/service") {
		serviceRoot = "/service"
	}
	serviceLink := filepath.Join(serviceRoot, plan.ServiceName)
	sourceDir := filepath.Join("/etc/sv", plan.ServiceName)
	commands, err := runServiceCommands(ctx, probe, []serviceCommand{
		{Args: []string{"sv", "down", serviceLink}, AllowFailure: true},
	})
	if removeErr := removeFileIfExists(serviceLink); removeErr != nil && err == nil {
		err = removeErr
	}
	if removeErr := removeAllIfExists(sourceDir); removeErr != nil && err == nil {
		err = removeErr
	}
	return Result{ServicePath: filepath.Join(sourceDir, "run"), Commands: commands}, err
}

func installUpstart(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/etc/init", plan.ServiceName+".conf")
	content := fmt.Sprintf(`description "Sitebrush server"
start on filesystem and net-device-up IFACE!=lo
stop on runlevel [016]
respawn
chdir %s
exec %s
`, plan.WorkingDir, execLine(plan.ExecArgs))
	if err := writeFile(path, content, 0o644); err != nil {
		return Result{}, err
	}
	commands, err := runInstallCommands(ctx, probe, [][]string{
		{"initctl", "reload-configuration"},
		{"initctl", "restart", plan.ServiceName},
		{"initctl", "status", plan.ServiceName},
	})
	if err != nil && strings.Contains(err.Error(), "restart") {
		commands, err = runInstallCommands(ctx, probe, [][]string{
			{"initctl", "reload-configuration"},
			{"initctl", "start", plan.ServiceName},
			{"initctl", "status", plan.ServiceName},
		})
	}
	return Result{ServicePath: path, Commands: commands}, err
}

func uninstallUpstart(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/etc/init", plan.ServiceName+".conf")
	commands, err := runServiceCommands(ctx, probe, []serviceCommand{
		{Args: []string{"initctl", "stop", plan.ServiceName}, AllowFailure: true},
	})
	if removeErr := removeFileIfExists(path); removeErr != nil && err == nil {
		err = removeErr
	}
	if err == nil {
		extraCommands, commandErr := runInstallCommands(ctx, probe, [][]string{{"initctl", "reload-configuration"}})
		commands = append(commands, extraCommands...)
		err = commandErr
	}
	return Result{ServicePath: path, Commands: commands}, err
}

func installSysVInit(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/etc/init.d", plan.ServiceName)
	content := sysVRcScript(plan)
	if err := writeFile(path, content, 0o755); err != nil {
		return Result{}, err
	}
	enableCommand := []string{"update-rc.d", plan.ServiceName, "defaults"}
	if commandExists(probe, "chkconfig") {
		enableCommand = []string{"chkconfig", "--add", plan.ServiceName}
	} else if commandExists(probe, "insserv") {
		enableCommand = []string{"insserv", plan.ServiceName}
	}
	commands, err := runInstallCommands(ctx, probe, [][]string{
		enableCommand,
		{"service", plan.ServiceName, "restart"},
		{"service", plan.ServiceName, "status"},
	})
	return Result{ServicePath: path, Commands: commands}, err
}

func uninstallSysVInit(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/etc/init.d", plan.ServiceName)
	disableCommand := []string{"update-rc.d", "-f", plan.ServiceName, "remove"}
	if commandExists(probe, "chkconfig") {
		disableCommand = []string{"chkconfig", "--del", plan.ServiceName}
	} else if commandExists(probe, "insserv") {
		disableCommand = []string{"insserv", "-r", plan.ServiceName}
	}
	commands, err := runServiceCommands(ctx, probe, []serviceCommand{
		{Args: []string{"service", plan.ServiceName, "stop"}, AllowFailure: true},
		{Args: disableCommand, AllowFailure: true},
	})
	if removeErr := removeFileIfExists(path); removeErr != nil && err == nil {
		err = removeErr
	}
	return Result{ServicePath: path, Commands: commands}, err
}

func sysVRcScript(plan installPlan) string {
	return fmt.Sprintf(`#!/bin/sh
### BEGIN INIT INFO
# Provides:          %s
# Required-Start:    $remote_fs $network
# Required-Stop:     $remote_fs $network
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: Sitebrush server
### END INIT INFO

case "$1" in
  start)
    cd %s || exit 1
    nohup %s >/var/log/%s.log 2>&1 &
    echo $! >/var/run/%s.pid
    ;;
  stop)
    if [ -f /var/run/%s.pid ]; then kill "$(cat /var/run/%s.pid)" 2>/dev/null || true; rm -f /var/run/%s.pid; fi
    ;;
  restart)
    "$0" stop
    "$0" start
    ;;
  status)
    test -f /var/run/%s.pid && kill -0 "$(cat /var/run/%s.pid)" 2>/dev/null
    ;;
  *)
    echo "Usage: $0 {start|stop|restart|status}"
    exit 1
    ;;
esac
exit $?
`, plan.ServiceName, shellQuote(plan.WorkingDir), execLine(plan.ExecArgs), plan.ServiceName, plan.ServiceName, plan.ServiceName, plan.ServiceName, plan.ServiceName, plan.ServiceName, plan.ServiceName)
}

func installLaunchd(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	label := "net.sitebrush." + plan.ServiceName
	path := filepath.Join("/Library/LaunchDaemons", label+".plist")
	content := launchdPlist(label, plan)
	if err := writeFile(path, content, 0o644); err != nil {
		return Result{}, err
	}
	commands, err := runServiceCommands(ctx, probe, []serviceCommand{
		{Args: []string{"launchctl", "bootout", "system/" + label}, AllowFailure: true},
		{Args: []string{"launchctl", "bootout", "system", path}, AllowFailure: true},
		{Args: []string{"launchctl", "enable", "system/" + label}, AllowFailure: true},
		{Args: []string{"launchctl", "bootstrap", "system", path}},
	})
	if err != nil {
		retryCommands, retryErr := runServiceCommands(ctx, probe, []serviceCommand{
			{Args: []string{"launchctl", "bootout", "system/" + label}, AllowFailure: true},
			{Args: []string{"launchctl", "bootout", "system", path}, AllowFailure: true},
			{Args: []string{"launchctl", "enable", "system/" + label}, AllowFailure: true},
			{Args: []string{"launchctl", "bootstrap", "system", path}},
		})
		commands = append(commands, retryCommands...)
		err = retryErr
	}
	if err == nil {
		extraCommands, extraErr := runServiceCommands(ctx, probe, []serviceCommand{
			{Args: []string{"launchctl", "enable", "system/" + label}},
			{Args: []string{"launchctl", "kickstart", "-k", "system/" + label}},
			{Args: []string{"launchctl", "print", "system/" + label}},
		})
		commands = append(commands, extraCommands...)
		err = extraErr
	}
	return Result{ServicePath: path, Commands: commands}, err
}

func uninstallLaunchd(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	label := "net.sitebrush." + plan.ServiceName
	path := filepath.Join("/Library/LaunchDaemons", label+".plist")
	commands, err := runServiceCommands(ctx, probe, []serviceCommand{
		{Args: []string{"launchctl", "bootout", "system/" + label}, AllowFailure: true},
		{Args: []string{"launchctl", "bootout", "system", path}, AllowFailure: true},
	})
	if removeErr := removeFileIfExists(path); removeErr != nil && err == nil {
		err = removeErr
	}
	return Result{ServicePath: path, Commands: commands}, err
}

func launchdPlist(label string, plan installPlan) string {
	args := make([]string, 0, len(plan.ExecArgs))
	for _, arg := range plan.ExecArgs {
		args = append(args, fmt.Sprintf("		<string>%s</string>", escapeXML(arg)))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s
	</array>
	<key>WorkingDirectory</key>
	<string>%s</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>/var/log/%s.log</string>
	<key>StandardErrorPath</key>
	<string>/var/log/%s.log</string>
</dict>
</plist>
`, label, strings.Join(args, "\n"), escapeXML(plan.WorkingDir), plan.ServiceName, plan.ServiceName)
}

func escapeXML(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	return value
}

func installWindowsService(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	serviceName := plan.ServiceName
	binPath := windowsCommandLine(plan.ExecArgs)
	commands, err := runInstallCommands(ctx, probe, [][]string{
		{"sc.exe", "query", serviceName},
	})
	if err != nil {
		commands, err = runInstallCommands(ctx, probe, [][]string{
			{"sc.exe", "create", serviceName, "binPath=", binPath, "start=", "auto", "DisplayName=", "Sitebrush"},
			{"sc.exe", "failure", serviceName, "reset=", "60", "actions=", "restart/5000/restart/5000/"},
			{"sc.exe", "start", serviceName},
			{"sc.exe", "query", serviceName},
		})
	} else {
		commands, err = runInstallCommands(ctx, probe, [][]string{
			{"sc.exe", "config", serviceName, "binPath=", binPath, "start=", "auto"},
			{"sc.exe", "failure", serviceName, "reset=", "60", "actions=", "restart/5000/restart/5000/"},
			{"sc.exe", "start", serviceName},
			{"sc.exe", "query", serviceName},
		})
	}
	return Result{ServicePath: "Windows Service: " + serviceName, Commands: commands}, err
}

func uninstallWindowsService(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	serviceName := plan.ServiceName
	commands, err := runServiceCommands(ctx, probe, []serviceCommand{
		{Args: []string{"sc.exe", "stop", serviceName}, AllowFailure: true},
		{Args: []string{"sc.exe", "config", serviceName, "start=", "disabled"}, AllowFailure: true},
		{Args: []string{"sc.exe", "delete", serviceName}},
	})
	return Result{ServicePath: "Windows Service: " + serviceName, Commands: commands}, err
}

func installFreeBSDRcD(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/usr/local/etc/rc.d", plan.ServiceName)
	if err := writeFile(path, freeBSDRcScript(plan), 0o755); err != nil {
		return Result{}, err
	}
	enableCommands := bsdEnableCommands(probe, plan.ServiceName, "YES")
	commands := append(enableCommands, []string{"service", plan.ServiceName, "restart"}, []string{"service", plan.ServiceName, "status"})
	executed, err := runInstallCommands(ctx, probe, commands)
	return Result{ServicePath: path, Commands: executed}, err
}

func uninstallFreeBSDRcD(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/usr/local/etc/rc.d", plan.ServiceName)
	commands := []serviceCommand{
		{Args: []string{"service", plan.ServiceName, "stop"}, AllowFailure: true},
	}
	if commandExists(probe, "sysrc") {
		commands = append(commands, serviceCommand{Args: []string{"sysrc", "-x", plan.ServiceName + "_enable"}, AllowFailure: true})
	} else if err := removeRcConfLines(plan.ServiceName + "_enable"); err != nil {
		return Result{}, err
	}
	executed, err := runServiceCommands(ctx, probe, commands)
	if removeErr := removeFileIfExists(path); removeErr != nil && err == nil {
		err = removeErr
	}
	return Result{ServicePath: path, Commands: executed}, err
}

func installOpenBSDRcD(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/etc/rc.d", plan.ServiceName)
	if err := writeFile(path, openBSDRcScript(plan), 0o755); err != nil {
		return Result{}, err
	}
	commands, err := runInstallCommands(ctx, probe, [][]string{
		{"rcctl", "enable", plan.ServiceName},
		{"rcctl", "restart", plan.ServiceName},
		{"rcctl", "check", plan.ServiceName},
	})
	return Result{ServicePath: path, Commands: commands}, err
}

func uninstallOpenBSDRcD(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/etc/rc.d", plan.ServiceName)
	commands, err := runServiceCommands(ctx, probe, []serviceCommand{
		{Args: []string{"rcctl", "stop", plan.ServiceName}, AllowFailure: true},
		{Args: []string{"rcctl", "disable", plan.ServiceName}, AllowFailure: true},
	})
	if removeErr := removeFileIfExists(path); removeErr != nil && err == nil {
		err = removeErr
	}
	return Result{ServicePath: path, Commands: commands}, err
}

func installNetBSDRcD(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/etc/rc.d", plan.ServiceName)
	if err := writeFile(path, netBSDRcScript(plan), 0o755); err != nil {
		return Result{}, err
	}
	if err := appendRcConf(plan.ServiceName + "=YES\n"); err != nil {
		return Result{}, err
	}
	commands, err := runInstallCommands(ctx, probe, [][]string{
		{"/etc/rc.d/" + plan.ServiceName, "restart"},
		{"/etc/rc.d/" + plan.ServiceName, "status"},
	})
	return Result{ServicePath: path, Commands: commands}, err
}

func uninstallNetBSDRcD(ctx context.Context, probe runtimeProbe, plan installPlan) (Result, error) {
	path := filepath.Join("/etc/rc.d", plan.ServiceName)
	commands, err := runServiceCommands(ctx, probe, []serviceCommand{
		{Args: []string{path, "stop"}, AllowFailure: true},
	})
	if removeErr := removeRcConfLines(plan.ServiceName); removeErr != nil && err == nil {
		err = removeErr
	}
	if removeErr := removeFileIfExists(path); removeErr != nil && err == nil {
		err = removeErr
	}
	return Result{ServicePath: path, Commands: commands}, err
}

func bsdEnableCommands(probe runtimeProbe, serviceName, value string) [][]string {
	if commandExists(probe, "sysrc") {
		return [][]string{{"sysrc", serviceName + "_enable=" + value}}
	}
	_ = appendRcConf(fmt.Sprintf("%s_enable=\"%s\"\n", serviceName, value))
	return nil
}

func appendRcConf(line string) error {
	content, _ := os.ReadFile("/etc/rc.conf")
	if strings.Contains(string(content), strings.TrimSpace(line)) {
		return nil
	}
	file, err := os.OpenFile("/etc/rc.conf", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("update /etc/rc.conf: %w", err)
	}
	defer file.Close()
	_, err = file.WriteString(line)
	return err
}

func removeRcConfLines(key string) error {
	content, err := os.ReadFile("/etc/rc.conf")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read /etc/rc.conf: %w", err)
	}
	lines := strings.Split(string(content), "\n")
	kept := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+"=") || strings.HasPrefix(trimmed, key+"_enable=") {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	if !changed {
		return nil
	}
	output := strings.Join(kept, "\n")
	if strings.TrimSpace(output) != "" && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	// #nosec G703 -- /etc/rc.conf is a fixed operating-system integration path.
	if err := os.WriteFile("/etc/rc.conf", []byte(output), 0o644); err != nil {
		return fmt.Errorf("write /etc/rc.conf: %w", err)
	}
	return nil
}

func freeBSDRcScript(plan installPlan) string {
	return fmt.Sprintf(`#!/bin/sh
# PROVIDE: %s
# REQUIRE: NETWORKING
# KEYWORD: shutdown

. /etc/rc.subr

name="%s"
rcvar="${name}_enable"
command=%s
command_args=%s
pidfile="/var/run/${name}.pid"
start_cmd="${name}_start"
stop_cmd="${name}_stop"

%s_start()
{
	cd %s || exit 1
	/usr/sbin/daemon -p "${pidfile}" -f "${command}" ${command_args}
}

%s_stop()
{
	if [ -f "${pidfile}" ]; then kill "$(cat "${pidfile}")" 2>/dev/null || true; rm -f "${pidfile}"; fi
}

load_rc_config $name
: ${%s_enable:="NO"}
run_rc_command "$1"
`, plan.ServiceName, plan.ServiceName, shellQuote(plan.BinaryPath), shellQuote(strings.Join(plan.ExecArgs[1:], " ")), plan.ServiceName, shellQuote(plan.WorkingDir), plan.ServiceName, plan.ServiceName)
}

func openBSDRcScript(plan installPlan) string {
	return fmt.Sprintf(`#!/bin/ksh
daemon=%s
daemon_flags=%s
daemon_user=root

. /etc/rc.d/rc.subr

rc_bg=YES
rc_reload=NO
pexp="${daemon}.*"
rc_cmd $1
`, shellQuote(plan.BinaryPath), shellQuote(strings.Join(plan.ExecArgs[1:], " ")))
}

func netBSDRcScript(plan installPlan) string {
	return fmt.Sprintf(`#!/bin/sh
# PROVIDE: %s
# REQUIRE: NETWORKING
# KEYWORD: shutdown

$_rc_subr_loaded . /etc/rc.subr

name="%s"
rcvar=$name
command=%s
command_args=%s
pidfile="/var/run/${name}.pid"
start_cmd="${name}_start"
stop_cmd="${name}_stop"

%s_start()
{
	cd %s || exit 1
	/usr/sbin/daemon -p "${pidfile}" "${command}" ${command_args}
}

%s_stop()
{
	if [ -f "${pidfile}" ]; then kill "$(cat "${pidfile}")" 2>/dev/null || true; rm -f "${pidfile}"; fi
}

load_rc_config $name
run_rc_command "$1"
`, plan.ServiceName, plan.ServiceName, shellQuote(plan.BinaryPath), shellQuote(strings.Join(plan.ExecArgs[1:], " ")), plan.ServiceName, shellQuote(plan.WorkingDir), plan.ServiceName)
}

func printResult(out io.Writer, result Result, languageCode string) {
	if out == nil {
		return
	}
	theme := resolveCLITheme(out)
	text := cliTextForLanguage(languageCode)
	fmt.Fprintf(out, "%s\n", theme.success(text.InstallComplete))
	fmt.Fprintf(out, "%s: %s %s (%s)\n", text.OSLabel, result.OS, result.OSVersion, result.Arch)
	fmt.Fprintf(out, "%s: %s\n", text.ServiceSystemLabel, result.InitSystem)
	fmt.Fprintf(out, "%s: %s\n", text.BinaryPathLabel, result.BinaryPath)
	fmt.Fprintf(out, "%s: %s\n", text.ServiceFileLabel, result.ServicePath)
	if len(result.Commands) > 0 {
		fmt.Fprintf(out, "%s:\n", text.CommandsLabel)
		for _, command := range result.Commands {
			fmt.Fprintf(out, "  %s\n", command)
		}
	}
}

func printUninstallResult(out io.Writer, result Result, languageCode string) {
	if out == nil {
		return
	}
	theme := resolveCLITheme(out)
	text := cliTextForLanguage(languageCode)
	fmt.Fprintf(out, "%s\n", theme.success(text.UninstallComplete))
	fmt.Fprintf(out, "%s: %s %s (%s)\n", text.OSLabel, result.OS, result.OSVersion, result.Arch)
	fmt.Fprintf(out, "%s: %s\n", text.ServiceSystemLabel, result.InitSystem)
	fmt.Fprintf(out, "%s: %s\n", text.ServiceFileLabel, result.ServicePath)
	if strings.TrimSpace(result.BinaryPath) != "" {
		fmt.Fprintf(out, "%s: %s\n", text.BinaryLeftLabel, result.BinaryPath)
	}
	if len(result.Commands) > 0 {
		fmt.Fprintf(out, "%s:\n", text.CommandsLabel)
		for _, command := range result.Commands {
			fmt.Fprintf(out, "  %s\n", command)
		}
	}
}
