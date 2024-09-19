// /usr/bin/true; exec go run "$0" "$@"
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const DEFAULT_PERMISSION = 0755
const ENV_HOME = "HOME"
const ENV_XDG_CONFIG_HOME = "XDG_CONFIG_HOME"
const ENV_XDG_DATA_HOME = "XDG_DATA_HOME"
const ENV_XDG_CACHE_HOME = "XDG_CACHE_HOME"

const APP_NAME = "plauncher"
const STEAMAPPID_FILENAME = "steam_appid.txt"
const COMMON_STEAM_APP_NAME = "Common"

const GAMEMODE_BIN_NAME = "gamemoderun"
const MANGOHUD_BIN_NAME = "mangohud"
const MANGOAPP_BIN_NAME = "mangoapp"
const GAMESCOPE_BIN_NAME = "gamescope"
const LEGENDARY_BIN_NAME = "legendary"
const UMU_RUN_BIN_NAME = "umu-run"

const GAMESCOPE_MANGOAPP_ARGV = "--mangoapp"
const GAMESCOPE_HDR_ARGV = "--hdr-enabled"

var gameExeRegex = regexp.MustCompile("waitforexitandrun\\ (\\/.+(\\.exe|\\.bat))")
var steamAppidRegex = regexp.MustCompile("AppId=([0-9]+)")

type Configuration struct {
	Environment  map[string]string      `yaml:"environment"`
	Wine         WineConfiguration      `yaml:"wine"`
	Mangohud     MangohudConfiguration  `yaml:"mangohud"`
	Gamemode     GamemodeConfiguration  `yaml:"gamemode"`
	Gamescope    GamescopeConfiguration `yaml:"gamescope"`
	EosOverlay   EosConfiguration       `yaml:"eos-overlay"`
	Umu          UmuConfiguration       `yaml:"umu"`
	PreScripts   []string               `yaml:"pre-scripts"`
	PostScripts  []string               `yaml:"post-scripts"`
	specialFlags map[string]bool
	props        map[string]string
}

type WineConfiguration struct {
	Alsa bool `yaml:"alsa"`
}

type MangohudConfiguration struct {
	Enabled bool `yaml:"enabled"`
}

type GamemodeConfiguration struct {
	Enabled bool `yaml:"enabled"`
}

type GamescopeConfiguration struct {
	Enabled bool     `yaml:"enabled"`
	Hdr     bool     `yaml:"hdr"`
	Args    []string `yaml:"args"`
}

type EosConfiguration struct {
	Enabled bool `yaml:"enabled"`
}

type UmuConfiguration struct {
	Enabled bool     `yaml:"enabled"`
	Proton  string   `yaml:"proton"`
	GameId  string   `yaml:"game-id"`
	Store   string   `yaml:"store"`
	Args    []string `yaml:"args"`
}

type BasicSteamSpyResponse struct {
	AppId int    `json:"appid"`
	Name  string `json:"name"`
}

func main() {
	homeDir, homeDirErr := os.UserHomeDir()

	if homeDirErr != nil {
		log.Fatalf("Failed to determine user HOME folder: %s\n", homeDirErr)
	}

	userCacheDir, cacheDirErr := os.UserCacheDir()

	if cacheDirErr != nil {
		log.Fatalf("Failed to determine user CACHE folder: %s\n", cacheDirErr)
	}

	userConfigDir, configDirErr := os.UserConfigDir()

	if configDirErr != nil {
		log.Fatalf("Failed to determine user CONFIG folder: %s\n", configDirErr)
	}

	userDataDir := determineBaseDataDir(homeDir)

	baseAppConfigFolder := filepath.Join(userConfigDir, APP_NAME)
	compatDataBase := filepath.Join(userDataDir, APP_NAME, "compatdata")
	appNamesCacheFolder := filepath.Join(userCacheDir, APP_NAME, "appnames")
	appScriptsFolder := filepath.Join(baseAppConfigFolder, "scripts")
	gameOverridesFolder := filepath.Join(baseAppConfigFolder, "overrides")

	configurationFile := filepath.Join(baseAppConfigFolder, "config.yaml")
	debugFile := filepath.Join(userDataDir, APP_NAME, "debug.log")

	if _, err := os.Stat(debugFile); os.IsNotExist(err) {
		os.WriteFile(debugFile, []byte(""), DEFAULT_PERMISSION)
	}

	debugFileHandle, debugFileErr := os.OpenFile(debugFile, os.O_APPEND|os.O_RDWR|os.O_CREATE, DEFAULT_PERMISSION)

	if debugFileErr != nil {
		log.Fatalf("Failed to open DEBUG FILE: %s\n", debugFileErr)
	}

	defer debugFileHandle.Close()

	log.SetOutput(debugFileHandle)

	log.Printf("---------------------- START PID: %d ----------------------\n", os.Getpid())

	log.Printf("Using app names cache folder: %s\n", appNamesCacheFolder)
	log.Printf("Using scripts folder: %s\n", appScriptsFolder)
	log.Printf("Using game overrides folder: %s\n", gameOverridesFolder)

	log.Printf("Writing to debug file: %s\n", debugFile)
	log.Printf("Using configuration file: %s\n", configurationFile)

	makeSureFoldersExist(
		appNamesCacheFolder,
		appScriptsFolder,
		gameOverridesFolder,
	)

	plauncherShortcut := filepath.Join(homeDir, ".plauncher")

	if _, err := os.Lstat(plauncherShortcut); !os.IsNotExist(err) {
		os.Remove(plauncherShortcut)
	}

	os.Symlink(baseAppConfigFolder, plauncherShortcut)

	defaultConfiguration := Configuration{
		make(map[string]string),
		WineConfiguration{true},
		MangohudConfiguration{false},
		GamemodeConfiguration{true},
		GamescopeConfiguration{false, false, make([]string, 0)},
		EosConfiguration{false},
		UmuConfiguration{false, "", "", "", make([]string, 0)},
		make([]string, 0),
		make([]string, 0),
		make(map[string]bool),
		make(map[string]string),
	}

	userConfiguration := readOrCreateUserConfiguration(defaultConfiguration, configurationFile)
	indexFirstNonFlagArg, enrichErr := enrichConfigurationWithArgvFlags(&userConfiguration)

	if enrichErr != nil {
		log.Fatal(enrichErr)
	}

	nonFlagArgs := os.Args[indexFirstNonFlagArg:]
	nonFlagsArgsString := strings.Join(nonFlagArgs, " ")

	if oldSteamCompatData, exists := os.LookupEnv("STEAM_COMPAT_DATA_PATH"); exists {
		log.Println("Detected steam compat data variables")
		enrichSteamAppIdByExe(&userConfiguration, nonFlagsArgsString)
		enrichSteamAppIdByArgs(&userConfiguration, nonFlagsArgsString)
		enrichGameName(&userConfiguration, appNamesCacheFolder)
		configureNewSteamCompatData(&userConfiguration, oldSteamCompatData, homeDir, compatDataBase)
	}

	gameOverrideByNameFile := filepath.Join(gameOverridesFolder, userConfiguration.props["name"]+".yaml")
	gameOverrideByIdFile := filepath.Join(gameOverridesFolder, userConfiguration.props["id"]+".yaml")

	if _, err := os.Stat(gameOverrideByNameFile); !os.IsNotExist(err) {
		log.Printf("Found game name override file: %s\n", gameOverrideByNameFile)
		applyConfigOverrides(&userConfiguration, readOrCreateUserConfiguration(defaultConfiguration, gameOverrideByNameFile))
	}

	if _, err := os.Stat(gameOverrideByIdFile); !os.IsNotExist(err) {
		log.Printf("Found game id override file: %s\n", gameOverrideByIdFile)
		applyConfigOverrides(&userConfiguration, readOrCreateUserConfiguration(defaultConfiguration, gameOverrideByIdFile))
	}

	setupEosInPrefix(userConfiguration, filepath.Join(userDataDir, APP_NAME))
	//setupWineConfigInPrefix(userConfiguration, compatDataBase)

	command := make([]string, 0)

	command = enrichCommandWithMangohud(command, &userConfiguration, userConfigDir)
	command = enrichCommandWithGamemode(command, &userConfiguration)
	command = enrichCommandWithGamescope(command, &userConfiguration, userConfigDir)
	command = enrichCommandWithUmu(command, &userConfiguration, compatDataBase)
	command = append(command, nonFlagArgs...)

	finalConfigurationYaml, _ := yaml.Marshal(userConfiguration)

	log.Printf("Final configuration: \n%s\n", finalConfigurationYaml)

	cmdHandle := exec.Command(command[0], command[1:]...)
	newEnviron := os.Environ()

	for key, value := range userConfiguration.Environment {
		newEnviron = append(newEnviron, fmt.Sprintf("%s=%s", key, os.ExpandEnv(value)))
	}

	cmdHandle.Env = newEnviron

	processSpecialFlags(userConfiguration.specialFlags, userConfiguration, gameOverridesFolder)

	executeScripts(userConfiguration.PreScripts, appScriptsFolder)

	log.Printf("Executing: %s\n", command)

	if out, err := cmdHandle.Output(); err != nil {
		log.Printf("Command stopped: %s. Error: %s", out, err)
		executeScripts(userConfiguration.PostScripts, appScriptsFolder)
		log.Fatalf("---------------------- END PID: %d ----------------------\n", os.Getpid())
	}

	executeScripts(userConfiguration.PostScripts, appScriptsFolder)
	log.Printf("---------------------- END PID: %d ----------------------\n", os.Getpid())
}

func determineBaseDataDir(home string) string {
	xdgDataHomeValue, xdgDataHomeExists := os.LookupEnv(ENV_XDG_DATA_HOME)

	if xdgDataHomeExists {
		return xdgDataHomeValue
	}

	return filepath.Join(home, ".local", "share")
}

func makeSureFoldersExist(folders ...string) {
	for _, folder := range folders {
		if _, err := os.Stat(folder); os.IsNotExist(err) {
			os.MkdirAll(folder, DEFAULT_PERMISSION)
		}
	}
}

func readOrCreateUserConfiguration(defaultConfiguration Configuration, configurationFile string) Configuration {
	if _, err := os.Stat(configurationFile); os.IsNotExist(err) {
		defaultYaml, err := yaml.Marshal(defaultConfiguration)
		if err != nil {
			log.Fatal(err)
		}
		os.WriteFile(configurationFile, defaultYaml, DEFAULT_PERMISSION)
		return defaultConfiguration
	}

	configurationFileContent, err := os.ReadFile(configurationFile)

	if err != nil {
		log.Fatal(err)
	}

	userConfiguration := Configuration{}
	yamlErr := yaml.Unmarshal(configurationFileContent, &userConfiguration)

	if yamlErr != nil {
		log.Fatal(yamlErr)
	}

	if userConfiguration.Environment == nil {
		userConfiguration.Environment = make(map[string]string)
	}

	userConfiguration.specialFlags = make(map[string]bool)
	userConfiguration.props = make(map[string]string)

	return userConfiguration
}

func enrichConfigurationWithArgvFlags(configuration *Configuration) (int, error) {
	for i, arg := range os.Args {
		if i == 0 {
			continue
		}

		if strings.HasPrefix(arg, "--") {
			parseDoubleDashParam(configuration, arg[2:])
			continue
		}

		if strings.HasPrefix(arg, "-!") {
			parseBooleanDashParam(configuration, arg[2:], false)
			continue
		}

		if strings.HasPrefix(arg, "-") {
			parseBooleanDashParam(configuration, arg[1:], true)
			continue
		}

		return i, nil
	}

	return -1, errors.New(fmt.Sprintf("Could not find command in: %s", os.Args))
}

func parseDoubleDashParam(configuration *Configuration, arg string) {
	if strings.Contains(arg, "=") {
		split_arg := strings.Split(arg, "=")
		configuration.props[split_arg[0]] = split_arg[1]
		return
	}

	configuration.specialFlags[arg] = true
}

func parseBooleanDashParam(configuration *Configuration, arg string, value bool) {
	for _, char := range arg {
		switch char {
		case 'G':
			configuration.Gamescope.Enabled = value
		case 'g':
			configuration.Gamemode.Enabled = value
		case 'h':
			configuration.Gamescope.Hdr = value
		case 'm':
			configuration.Mangohud.Enabled = value
		case 'e':
			configuration.EosOverlay.Enabled = value

		}
	}
}

func applyConfigOverrides(currentConfiguration *Configuration, overrideConfiguration Configuration) {
	for key, value := range overrideConfiguration.Environment {
		currentConfiguration.Environment[key] = value
	}

	currentConfiguration.Gamemode.Enabled = overrideConfiguration.Gamemode.Enabled
	currentConfiguration.Mangohud.Enabled = overrideConfiguration.Mangohud.Enabled

	currentConfiguration.Gamescope.Enabled = overrideConfiguration.Gamescope.Enabled
	currentConfiguration.Gamescope.Hdr = overrideConfiguration.Gamescope.Hdr

	currentConfiguration.EosOverlay.Enabled = overrideConfiguration.EosOverlay.Enabled

	currentConfiguration.Umu.Enabled = overrideConfiguration.Umu.Enabled

	currentConfiguration.Wine.Alsa = overrideConfiguration.Wine.Alsa

	if overrideConfiguration.Umu.Proton != "" {
		currentConfiguration.Umu.Proton = overrideConfiguration.Umu.Proton
	}

	if overrideConfiguration.Umu.Store != "" {
		currentConfiguration.Umu.Store = overrideConfiguration.Umu.Store
	}

	if overrideConfiguration.Umu.GameId != "" {
		currentConfiguration.Umu.GameId = overrideConfiguration.Umu.GameId
	}

	for _, umuArg := range overrideConfiguration.Umu.Args {
		if !slices.Contains(currentConfiguration.Umu.Args, umuArg) {
			currentConfiguration.Umu.Args = append(currentConfiguration.Umu.Args, umuArg)
		}
	}

	for _, gamescopeArg := range overrideConfiguration.Gamescope.Args {
		if !slices.Contains(currentConfiguration.Gamescope.Args, gamescopeArg) {
			currentConfiguration.Gamescope.Args = append(currentConfiguration.Gamescope.Args, gamescopeArg)
		}
	}

	for _, preScript := range overrideConfiguration.PreScripts {
		if !slices.Contains(currentConfiguration.PreScripts, preScript) {
			currentConfiguration.PreScripts = append(currentConfiguration.PreScripts, preScript)
		}
	}

	for _, postScript := range overrideConfiguration.PostScripts {
		if !slices.Contains(currentConfiguration.PostScripts, postScript) {
			currentConfiguration.PostScripts = append(currentConfiguration.PostScripts, postScript)
		}
	}
}

func enrichCommandWithMangohud(currentCommand []string, configuration *Configuration, userConfigDir string) []string {
	if _, exists := checkIfBinExists(MANGOHUD_BIN_NAME); configuration.Mangohud.Enabled && exists {
		configuration.Environment["MANGOHUD_CONFIGFILE"] = filepath.Join(userConfigDir, "MangoHud", "MangoHud.conf")
		configuration.Environment["MANGOHUD"] = "1"
		configuration.Environment["DISABLE_MANGOAPP"] = "1"
		return currentCommand
	}

	return currentCommand
}

func enrichCommandWithGamemode(currentCommand []string, configuration *Configuration) []string {
	if cmd, exists := checkIfBinExists(GAMEMODE_BIN_NAME); configuration.Gamemode.Enabled && exists {
		return append(currentCommand, cmd)
	}

	return currentCommand
}

func enrichCommandWithGamescope(currentCommand []string, configuration *Configuration, userConfigDir string) []string {
	if cmd, exists := checkIfBinExists(GAMESCOPE_BIN_NAME); configuration.Gamescope.Enabled && exists {
		newCmd := append(currentCommand, cmd)

		if configuration.Gamescope.Hdr {
			if !slices.Contains(configuration.Gamescope.Args, GAMESCOPE_HDR_ARGV) {
				configuration.Gamescope.Args = append(configuration.Gamescope.Args, GAMESCOPE_HDR_ARGV)
				configuration.Environment["DXVK_HDR"] = "1"
				configuration.Environment["ENABLE_HDR_WSI"] = "1"
			}
		}

		/*
			if _, exists := checkIfBinExists(MANGOAPP_BIN_NAME); exists && configuration.Mangohud.Enabled {
				configuration.Environment["MANGOHUD_CONFIGFILE"] = filepath.Join(userConfigDir, "MangoHud", "MangoHud-GS.conf")
				configuration.Environment["MANGOHUD"] = "0"
				configuration.Environment["DISABLE_MANGOAPP"] = "0"
				if !slices.Contains(configuration.Gamescope.Args, GAMESCOPE_MANGOAPP_ARGV) {
					newCmd = append(newCmd, GAMESCOPE_MANGOAPP_ARGV)
				}
			}
		*/

		for _, arg := range configuration.Gamescope.Args {
			for _, splitArg := range strings.Split(arg, " ") {
				newCmd = append(newCmd, splitArg)
			}
		}

		newCmd = append(newCmd, "--")

		return newCmd
	}

	return currentCommand
}

func enrichCommandWithUmu(currentCommand []string, configuration *Configuration, compatDataBase string) []string {
	if umuBin, exists := checkIfBinExists(UMU_RUN_BIN_NAME); exists {
		if _, exists := os.LookupEnv("STEAM_COMPAT_DATA_PATH"); !exists && configuration.Umu.Enabled {
			if _, exists := configuration.props["name"]; !exists {
				log.Fatalln("Games outside steam need a name. Set with --name=$val")
			}

			if protonDir, err := os.Stat(configuration.Umu.Proton); os.IsNotExist(err) || !protonDir.IsDir() {
				log.Fatalln("Specified proton path is does not exist or is not a directory")
			}

			prefixBaseFolder := filepath.Join(compatDataBase, configuration.props["name"])
			os.MkdirAll(filepath.Join(prefixBaseFolder), DEFAULT_PERMISSION)

			if id, exists := configuration.props["id"]; exists && id != "" {
				configuration.Environment["GAMEID"] = configuration.props["id"]
			}

			if id, exists := configuration.Environment["GAMEID"]; !exists || id == "" {
				configuration.Environment["GAMEID"] = configuration.props["name"]
			}

			configuration.Environment["WINEPREFIX"] = prefixBaseFolder
			if configuration.Umu.GameId != "" {
				configuration.Environment["GAMEID"] = configuration.Umu.GameId
			}
			configuration.Environment["PROTONPATH"] = configuration.Umu.Proton
			configuration.Environment["STORE"] = configuration.Umu.Store

			currentCommand = append(currentCommand, umuBin)

			for _, arg := range configuration.Umu.Args {
				currentCommand = append(currentCommand, arg)
			}
		}
	}

	return currentCommand
}

func setupEosInPrefix(configuration Configuration, appDataFolder string) {
	if configuration.EosOverlay.Enabled {
		if steamCompatData, exists := configuration.Environment["STEAM_COMPAT_DATA_PATH"]; exists {
			if cmd, exists := checkIfBinExists(LEGENDARY_BIN_NAME); exists {
				overlayFolder := filepath.Join(appDataFolder, "eos-overlay")
				log.Printf("Installing eos-overlay in: %s\n", overlayFolder)
				cmdHandle := exec.Command("yes", "|", cmd, "eos-overlay", "install", "--path", overlayFolder)
				cmdHandle.Run()
				prefixFolder := filepath.Join(steamCompatData, "pfx")
				log.Printf("Enabling eos-overlay in: %s, for prefix: %s\n", overlayFolder, prefixFolder)
				cmdHandle = exec.Command(cmd, "eos-overlay", "enable", "--prefix", fmt.Sprintf("'%s'", prefixFolder))
				err := cmdHandle.Run()

				if err != nil {
					log.Fatalf("Failed to enable eos-overlay: %s", err)
				}
			}
		}
	}
}

func setupWineConfigInPrefix(configuration Configuration, compatDataBase string) {
	if name, exists := configuration.props["name"]; exists {
		currentAudioDriver := "pulse"

		prefixFolder := filepath.Join(compatDataBase, name, "pfx")
		wineTricksLogPath := filepath.Join(prefixFolder, "winetricks.log")

		if _, err := os.Stat(wineTricksLogPath); !os.IsNotExist(err) {
			lastLine, err := ReadLastLine(wineTricksLogPath)

			if err != nil {
				log.Fatalf("Failed to read winetricks log file\n")
			}

			log.Printf("Winetricks log last line: %s", lastLine)

			lineValues := strings.Split(lastLine, "=")

			if lineValues[0] == "sound" {
				currentAudioDriver = lineValues[1]
			}
		}

		if configuration.Wine.Alsa && currentAudioDriver == "pulse" {
			setupAudioDriverInWine(prefixFolder, "alsa")
			return
		}

		if !configuration.Wine.Alsa && currentAudioDriver == "alsa" {
			setupAudioDriverInWine(prefixFolder, "pulse")
			return
		}
	}
}

func setupAudioDriverInWine(prefixFolder string, driver string) {
	if cmd, exists := checkIfBinExists("winetricks"); exists {
		cmdHandle := exec.Command(cmd, "settings", fmt.Sprintf("sound=%s", driver))
		cmdHandle.Env = append(os.Environ(), fmt.Sprintf("%s=%s", "WINEPREFIX", prefixFolder))

		log.Printf("Updating %s with audio driver %s. Command: %s\n", prefixFolder, driver, cmdHandle)

		err := cmdHandle.Run()

		if err != nil {
			log.Fatalf("Could not enable %s in prefix\n", driver)
		}
	}
}

func checkIfBinExists(binName string) (string, bool) {
	cmd := exec.Command("which", binName)

	stdout, err := cmd.Output()

	if err != nil {
		return "", false
	}

	return strings.TrimSpace(strings.Split(string(stdout), "\n")[0]), true
}

func executeScripts(scripts []string, scriptsFolder string) {
	for _, script := range scripts {
		fullScriptPath := filepath.Join(scriptsFolder, script)
		log.Printf("Executing script: %s\n", fullScriptPath)
		cmdHandle := exec.Command(os.Getenv("SHELL"), script)
		cmdHandle.Run()
	}
}

func processSpecialFlags(specialFlags map[string]bool, configuration Configuration, gameOverridesFolder string) {
	if _, exists := specialFlags["save-name"]; exists {
		createNameOverrideFile(configuration, gameOverridesFolder)
	}

	if _, exists := specialFlags["save-id"]; exists {
		createIdOverrideFile(configuration, gameOverridesFolder)
	}
}

func createNameOverrideFile(configuration Configuration, gameOverridesFolder string) {
	if name, exists := configuration.props["name"]; exists {
		nameOverrideFile := filepath.Join(gameOverridesFolder, name+".yaml")

		if _, err := os.Stat(nameOverrideFile); os.IsNotExist(err) {
			stripUnecessaryData(&configuration)

			yamlData, err := yaml.Marshal(configuration)

			if err != nil {
				log.Fatalf("Failed to create configuration yaml: %s", err)
			}

			log.Printf("Creating name override file in %s, with value: \n%s\n", nameOverrideFile, yamlData)

			os.WriteFile(nameOverrideFile, yamlData, DEFAULT_PERMISSION)
		}
	}
}

func createIdOverrideFile(configuration Configuration, gameOverridesFolder string) {
	if id, exists := configuration.props["id"]; exists {
		idOverrideFile := filepath.Join(gameOverridesFolder, id+".yaml")

		if _, err := os.Stat(idOverrideFile); os.IsNotExist(err) {
			stripUnecessaryData(&configuration)

			yamlData, err := yaml.Marshal(configuration)

			if err != nil {
				log.Fatalf("Failed to create configuration yaml: %s", err)
			}

			log.Printf("Creating id override file in %s, with value: \n%s\n", idOverrideFile, yamlData)

			os.WriteFile(idOverrideFile, yamlData, DEFAULT_PERMISSION)
		}
	}
}

func stripUnecessaryData(configuration *Configuration) {
	delete(configuration.Environment, "STEAM_COMPAT_DATA_PATH")
	delete(configuration.Environment, "MANGOHUD")
	delete(configuration.Environment, "DISABLE_MANGOAPP")
	delete(configuration.Environment, "MANGOHUD_CONFIGFILE")
}
