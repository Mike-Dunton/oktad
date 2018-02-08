package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/havoc-io/go-keytar"
	"github.com/jessevdk/go-flags"
	"github.com/peterh/liner"
	"github.com/visionmedia/go-debug"
)

const VERSION = "0.8.0"
const SESSION_COOKIE = "__oktad_session_cookie"
const CREDENTIALS_USERNAME = "__oktad_username"
const CREDENTIALS_PASSWORD = "__oktad_password"

func main() {
	var opts struct {
		ConfigFile          string `short:"c" long:"config" description:"Path to config file"`
		PrintVersion        bool   `short:"v" long:"version" description:"Print version number and exit"`
		ForceNewCredentials bool   `short:"f" long:"force-new" description:"force new credentials"`
	}

	debug := debug.Debug("oktad:main")
	args, err := flags.Parse(&opts)

	if err != nil {
		return
	}

	if opts.PrintVersion {
		fmt.Printf("oktad v%s\n", VERSION)
		return
	}

	debug("loading configuration data")
	// try to load configuration
	oktaCfg, err := parseConfig(opts.ConfigFile)

	if err != nil {
		fmt.Println("Error reading config file!")
		debug("cfg read err: %s", err)
		return
	}

	if len(args) <= 0 {
		fmt.Println("Hey, that command won't actually do anything.\n\nSorry.")
		return
	}

	awsProfile := args[0]
	acfg, err := readAwsProfile(
		fmt.Sprintf("profile %s", awsProfile),
	)

	var skipSecondRole bool

	if err != nil {
		//fmt.Println("Error reading your AWS profile!")
		debug("error reading AWS profile: %s", err)
		if err == awsProfileNotFound {
			// if the AWS profile isn't found, we'll assume that
			// the user intends to run a command in the first account
			// behind their okta auth, rather than assuming role twice
			skipSecondRole = true
			fmt.Printf(
				"We couldn't find an AWS profile named %s,\nso we will AssumeRole into your base account.\n",
				awsProfile,
			)
			awsProfile = BASE_PROFILE_CREDS

			args = append([]string{BASE_PROFILE_CREDS}, args...)
		}
	}

	var sessionToken string
	var saml *OktaSamlResponse
	var cookies []*http.Cookie

	sessionToken, err = getSessionFromLogin(&oktaCfg, cookies)
	if err != nil {
		debug("sessionToken1 error was %s", err)
		return
	}

	saml, cookies, err = getSaml(&oktaCfg, cookies, sessionToken)
	if err != nil {
		fmt.Println("Enter in the next 2fa token.")
		//Lets try one more time.
		sessionToken, err = getSessionFromLogin(&oktaCfg, cookies)
		if err != nil {
			debug("sessionToken2 error was %s", err)
			return
		}

		saml, cookies, err = getSaml(&oktaCfg, cookies, sessionToken)
		if err != nil {
			fmt.Println("Error parsing SAML response")
			debug("We are out of trys :(. Error was %s", err)
			return
		}
	}

	mainCreds, mExp, err := assumeFirstRole(acfg, saml)
	if err != nil {
		fmt.Println("Error assuming first role!")
		debug("error was %s", err)
		return
	}

	var finalCreds *credentials.Credentials
	var fExp time.Time
	if !skipSecondRole {
		finalCreds, fExp, err = assumeDestinationRole(acfg, mainCreds)
		if err != nil {
			fmt.Println("Error assuming second role!")
			debug("error was %s", err)
			return
		}
	} else {
		finalCreds = mainCreds
		fExp = mExp
	}

	// all was good, so let's save credentials...
	err = storeCreds(awsProfile, finalCreds, fExp)
	if err != nil {
		debug("err storing credentials, %s", err)
	}

	debug("Everything looks good; launching your program...")
	err = prepAndLaunch(args, finalCreds)
	if err != nil {
		fmt.Println("Error launching program: ", err)
	}
}

func getSessionFromLogin(oktaCfg *OktaConfig, cookies []*http.Cookie) (string, error) {
	debug := debug.Debug("oktad:getSessionFromLogin")
	var user, pass string

	keystore, err := keytar.GetKeychain()
	if err != nil {
		debug("error was %s", err)
		return "", errors.New("failed to get keychain access")
	}

	user, err = keystore.GetPassword(APPNAME, CREDENTIALS_USERNAME)
	if err != nil {
		debug("error getting username from keychain: %s", err)
	}

	pass, err = keystore.GetPassword(APPNAME, CREDENTIALS_PASSWORD)
	if err != nil {
		debug("error getting password from keychain: %s", err)
	}

	if user != "" && pass != "" {
		debug("stored okta credentials found, attempting to use them")
		sessionToken, err := tryLogin(oktaCfg, user, pass, cookies)
		if err == nil {
			return sessionToken, err
		}
		debug("error authenticating with stored credentials: %s", err)
		user = ""
		pass = ""
		// give the user the chance to log in by typing in username/password
	} else {
		debug("stored okta credentials not found; will prompt for them")
	}

	err = keystore.DeletePassword(APPNAME, CREDENTIALS_USERNAME)
	if err != nil {
		debug("error deleting %s: %s", CREDENTIALS_USERNAME, err)
	}

	err = keystore.DeletePassword(APPNAME, CREDENTIALS_PASSWORD)
	if err != nil {
		debug("error deleting %s: %s", CREDENTIALS_PASSWORD, err)
	}

	user, pass, err = readUserPass()
	if err != nil {
		// if we got an error here, the user bailed on us
		debug("control-c caught in liner, probably")
		return "", errors.New("control-c")
	}

	if user == "" || pass == "" {
		return "", errors.New("Must supply a username and password")
	}

	sessionToken, err := tryLogin(oktaCfg, user, pass, cookies)
	if err == nil && sessionToken != "" {
		keystore.AddPassword(APPNAME, CREDENTIALS_USERNAME, user)
		if err != nil {
			debug("err storing username: %s", err)
		}
		keystore.AddPassword(APPNAME, CREDENTIALS_PASSWORD, pass)
		if err != nil {
			debug("err storing password: %s", err)
		}
	}
	return sessionToken, err
}

func tryLogin(oktaCfg *OktaConfig, user string, pass string, cookies []*http.Cookie) (string, error) {
	debug := debug.Debug("oktad:tryLogin")
	ores, err := login(oktaCfg, user, pass, cookies)
	if err != nil {
		fmt.Println("Error authenticating with Okta! Maybe your username or password are wrong.")
		debug("login err %s", err)
		return "", err
	}

	if ores.Status == "SUCCESS" {
		return ores.SessionToken, nil
	}

	if ores.Status != "MFA_REQUIRED" {
		return "", errors.New("MFA required to use this tool")
	}

	factor, err := extractTokenFactor(ores)

	if err != nil {
		fmt.Println("Error processing okta response!")
		debug("err from extractTokenFactor was %s", err)
		return "", err
	}

	tries := 0
	var sessionToken string

TRYMFA:
	mfaToken, err := readMfaToken()
	if err != nil {
		debug("control-c caught in liner, probably")
		return "", err
	}

	if tries < 2 {
		sessionToken, err = doMfa(ores, factor, mfaToken, cookies)
		if err != nil {
			tries++
			fmt.Println("Invalid MFA code, please try again.")
			goto TRYMFA // eat that, Djikstra!
		}
	} else {
		fmt.Println("Error performing MFA auth!")
		debug("error from doMfa was %s", err)
		return "", err
	}

	return sessionToken, nil
}

// reads the username and password from the command line
// returns user, then pass, then an error
func readUserPass() (user string, pass string, err error) {
	li := liner.NewLiner()

	// remember to close or weird stuff happens
	defer li.Close()

	li.SetCtrlCAborts(true)
	user, err = li.Prompt("Username: ")
	if err != nil {
		return
	}

	pass, err = li.PasswordPrompt("Password: ")
	if err != nil {
		return
	}

	return
}

// reads and returns an mfa token
func readMfaToken() (string, error) {
	li := liner.NewLiner()
	defer li.Close()
	li.SetCtrlCAborts(true)
	fmt.Println("Your account requires MFA; please enter a token.")
	return li.Prompt("MFA token: ")
}
