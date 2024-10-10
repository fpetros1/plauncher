package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func enrichSteamAppIdByExe(configuration *Configuration, nonFlagsArgsString string) {
	if _, exists := configuration.props["steam-appid"]; !exists {
		gameExeMatchResult := gameExeRegex.FindStringSubmatch(nonFlagsArgsString)

		if gameExeMatchResult != nil {
			gameExec := gameExeMatchResult[1]
			gameFolder := filepath.Dir(gameExec)
			appIdFile := filepath.Join(gameFolder, STEAMAPPID_FILENAME)

			if _, err := os.Stat(appIdFile); !os.IsNotExist(err) {
				file, err := os.Open(appIdFile)
				if err != nil {
					log.Fatalf("Failed to open file: %s\n", err)
					return
				}
				defer file.Close()

				scanner := bufio.NewScanner(file)
				scanner.Split(bufio.ScanLines)

				for scanner.Scan() {

					configuration.props["steam-appid"] = scanner.Text()
					configuration.props["id"] = scanner.Text()
				}
			}
		}
	}
}

func enrichSteamAppIdByArgs(configuration *Configuration, nonFlagsArgsString string) {
	if _, exists := configuration.props["steam-appid"]; !exists {
		steamAppidRegexResult := steamAppidRegex.FindStringSubmatch(nonFlagsArgsString)

		if steamAppidRegexResult != nil {
			configuration.props["steam-appid"] = steamAppidRegexResult[1]
			configuration.props["id"] = steamAppidRegexResult[1]
		}
	}
}

func enrichGameName(configuration *Configuration, cacheFolder string) {
	if _, exists := configuration.props["steam-appid"]; exists {
		configuration.props["name"] = findSteamGameName(configuration.props["steam-appid"], cacheFolder)
		return
	}
}

func configureNewSteamCompatData(configuration *Configuration, oldCompatData string, homeDir string, newCompatDataBase string) {
	newCompatData := filepath.Join(newCompatDataBase, configuration.props["name"])
	compatDataBaseShortcut := filepath.Join(homeDir, ".compatdata")

	if _, err := os.Lstat(compatDataBaseShortcut); !os.IsNotExist(err) {
		os.Remove(compatDataBaseShortcut)
	}

	os.Symlink(newCompatDataBase, compatDataBaseShortcut)

	oldSteamCompatDataStats, oldCompatErr := os.Lstat(oldCompatData)
	_, newCompatErr := os.Stat(newCompatData)

	if os.IsNotExist(newCompatErr) && !os.IsNotExist(oldCompatErr) && oldSteamCompatDataStats.IsDir() {
		copyOldCompatDataToNew(configuration, oldCompatData, newCompatData)
		return
	}

	if !os.IsNotExist(newCompatErr) && !os.IsNotExist(oldCompatErr) && oldSteamCompatDataStats.IsDir() {
		os.RemoveAll(oldCompatData)
		os.Symlink(newCompatData, oldCompatData)
		configuration.Environment["STEAM_COMPAT_DATA_PATH"] = newCompatData

		log.Printf("Old compat data folder: %s\n", oldCompatData)
		log.Printf("New compat data folder: %s\n", newCompatData)
		return
	}

	os.Remove(oldCompatData)
	os.Symlink(newCompatData, oldCompatData)

	configuration.Environment["STEAM_COMPAT_DATA_PATH"] = newCompatData

	log.Printf("Old compat data folder: %s\n", oldCompatData)
	log.Printf("New compat data folder: %s\n", newCompatData)
}

func copyOldCompatDataToNew(configuration *Configuration, oldCompatData string, newCompatData string) {
	if err := CopyDir(oldCompatData, newCompatData); err != nil {
		log.Fatalf("Failed to copy compat data: %s", err)
	}

	if err := os.RemoveAll(oldCompatData); err != nil {
		log.Fatalf("Failed to delete old compat data: %s", err)
	}

	os.Symlink(newCompatData, oldCompatData)
	configuration.Environment["STEAM_COMPAT_DATA_PATH"] = newCompatData

	log.Printf("Old compat data folder: %s\n", oldCompatData)
	log.Printf("New compat data folder: %s\n", newCompatData)
}

func findSteamGameName(appid string, cacheFolder string) string {
	cacheFile := filepath.Join(cacheFolder, appid)

	if appName, err := os.ReadFile(cacheFile); !os.IsNotExist(err) {
		log.Printf("Fetching game name from cache file: %s\n", cacheFile)
		return string(appName)
	}

	log.Println("Game name cache file not available, fetching from SteamSpy")

	resp, err := http.Get(fmt.Sprintf("https://steamspy.com/api.php?request=appdetails&appid=%s", appid))

	if err != nil {
		log.Fatalf("Could not fetch steam game name: %s\n", err)
	}

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		log.Fatalf("Could not read steamspy response: %s\n", err)
	}

	steamSpyResponse := BasicSteamSpyResponse{}
	err = json.Unmarshal(body, &steamSpyResponse)

	if err != nil {
		log.Fatalf("Steamspy response is not valid JSON: %s\n", err)
	}

	log.Printf("Saving game name(%s) in cache file: %s\n", steamSpyResponse.Name, cacheFile)
	err = os.WriteFile(cacheFile, []byte(steamSpyResponse.Name), DEFAULT_PERMISSION)

	if err != nil {
		log.Fatalf("Could not write cache file: %s\n", err)
	}

	return steamSpyResponse.Name
}
