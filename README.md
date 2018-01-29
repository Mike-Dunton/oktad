# oktad

[okta-aws](https://github.com/RedVentures/okta-aws), but in go. This program authenticates with Okta and then assumes role twice in Amazon.

# Installation

Clone the repo to `$GOPATH/src/Mike-Dunton/`
```
git clone git@github.com:Mike-Dunton/oktad.git
cd oktad
glide install
go build
mv oktad /some/path/that/is/in/your/$PATH
```


#testing signed commit 

## Setup

First, create an `~/.okta-aws/config` file with your Ookta base URL and app URL, like below:

```
[okta]
baseUrl=https://mycompany.okta.com/
appUrl=https://mycompany.okta.com/app/YOUR_APP/OKTA_MAGIC/sso/saml
```

Third, set up an AWS CLI config file. You need to create `~/.aws/config` and fill it with a profile containing the ARN for a role you ultimately want to get temporary credentials for. This file might look like the following:

```
[default]
output = json
region = us-east-1

[profile my_aws_profile]
role_arn = arn:aws:iam::MY_ACCOUNT_ID:role/wizards
```

With those things set up, you should be able to run `oktad my_subaccount -- [command]` to run whatever `[command]` is with a set of temporary credentials from Amazon.


## Usage

### Run a Command
This will run [command] with your aws credentials in the environment
```sh
$ oktad [AWS profile] -- [command]
```

for example

```sh
$ oktad my_aws_profile -- aws ec2 describe-vpcs
```

### Print your credentials
This will output your aws credentials.
```sh
oktad [AWS profile]
```

for example if you had a profile called "production"

```sh
oktad production
```

it will output the following
```
export AWS_SESSION_TOKEN=<snip>
export AWS_ACCESS_KEY_ID=<snip>
export AWS_SECRET_ACCESS_KEY=<snip>
```

## Useful one liners
```
eval "$(oktad my_aws_profile)"
```

## Debugging

Login didn't work? Launch this program with `DEBUG=oktad*` in your environment for more debugging info:

```sh
$ DEBUG=oktad* oktad production -- aws ec2 describe-instances
```

## Contributors

- Dimitrios Arethas [darethas@redventures.com]
- Thomas Hopkins [thopkins@redventures.com]
- Lee Standen [@lstanden]
- Todd Lunter [@tlunter]
- Michael Dunton [@Mike-Dunton]
