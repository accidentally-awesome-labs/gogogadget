package ui

// IconName is the icon registry key. Adding an icon = one const + one switch
// arm; TestIconRegistryIsComplete fails a const with no arm.
type IconName string

const (
	IconLogo         IconName = "logo"
	IconMenu         IconName = "menu"
	IconBell         IconName = "bell"
	IconSun          IconName = "sun"
	IconMoon         IconName = "moon"
	IconGlobe        IconName = "globe"
	IconCheck        IconName = "check"
	IconSpinner      IconName = "spinner"
	IconClose        IconName = "close"
	IconChevronDown  IconName = "chevron-down"
	IconChevronRight IconName = "chevron-right"
	IconChevronLeft  IconName = "chevron-left"
	IconChevronUp    IconName = "chevron-up"
	IconSearch       IconName = "search"
	IconAdd          IconName = "add"
	IconRemove       IconName = "remove"
	IconEdit         IconName = "edit"
	IconDelete       IconName = "delete"
	IconCopy         IconName = "copy"
	IconUpload       IconName = "upload"
	IconDownload     IconName = "download"
	IconFilter       IconName = "filter"
	IconSort         IconName = "sort"
	IconUser         IconName = "user"
	IconTeam         IconName = "team"
	IconOrg          IconName = "org"
	IconSettings     IconName = "settings"
	IconInfo         IconName = "info"
	IconWarn         IconName = "warn"
	IconError        IconName = "error"
	IconSuccess      IconName = "success"
	IconExternal     IconName = "external"
	IconMore         IconName = "more"
	IconCalendar     IconName = "calendar"
	IconClock        IconName = "clock"
	IconLink         IconName = "link"
	IconVisibility   IconName = "visibility"
	IconLock         IconName = "lock"
	IconKey          IconName = "key"
	IconRefresh      IconName = "refresh"
	IconPlay         IconName = "play"
	IconPause        IconName = "pause"
	IconArrowUp      IconName = "arrow-up"
	IconArrowDown    IconName = "arrow-down"
	IconArrowLeft    IconName = "arrow-left"
	IconArrowRight   IconName = "arrow-right"
	IconDrag         IconName = "drag"
	IconFile         IconName = "file"
	IconFolder       IconName = "folder"
	IconChart        IconName = "chart"
	IconCommand      IconName = "command"
	IconGrip         IconName = "grip"
)

// IconNames is every registered icon, in registry order. The gallery renders
// it and the registry test walks it.
var IconNames = []IconName{
	IconLogo, IconMenu, IconBell, IconSun, IconMoon, IconGlobe, IconCheck, IconSpinner,
	IconClose, IconChevronDown, IconChevronRight, IconChevronLeft, IconChevronUp,
	IconSearch, IconAdd, IconRemove, IconEdit, IconDelete, IconCopy, IconUpload,
	IconDownload, IconFilter, IconSort, IconUser, IconTeam, IconOrg, IconSettings,
	IconInfo, IconWarn, IconError, IconSuccess, IconExternal, IconMore,
	IconCalendar, IconClock, IconLink, IconVisibility, IconLock, IconKey,
	IconRefresh, IconPlay, IconPause, IconArrowUp, IconArrowDown, IconArrowLeft,
	IconArrowRight, IconDrag, IconFile, IconFolder, IconChart, IconCommand, IconGrip,
}
