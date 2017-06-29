# goprecheck
a tool for pre-checking go project built atop multiple open-source go tools 

### Install
```
go get -u github.com/caibirdme/goprecheck
```

### Install `Checkers`

* [gosimple]()
* [unused]()
* [golint]()

...(many others you want to use to check your project)

**Make sure all `Checkers` are added into PATH**

### Create TOML File

```toml
# show the progress,default false
show = true

# goroutines can be used
goroutines = 5

# what package you want to check
# or you can use -p the specify the packageName
# -p=foo has the higest priority 
packageName = "github.com/caibirdme/goprecheck"

# goprecheck will resolve all imported package recursively
# and only package that has prefix $packageName will be accounted
# packages in vendor directory will be ignored on default
# but if $filterRegxp is provided, only
# $package.HasPrefix($packageName) && $filterRegxp.Match(package) == true will be accounted
filterRegxp = "whatever u like"

[[checkers]]
    command="gosimple"
    #built-in flags for the command
    args = ["-tests=false"]
    # `gosimple -tests=false packageA packageB packageC ...` will be invoked
[[checkers]]
    command = "unused"
    args = ["-tests=false"]
[[checkers]]
    command =  "golint"
    args = ["-set_exit_status"]
    # golint only support one package as its param
    # see `golint -h`
    onePackage = true

```

### Flags

* -conf (specify the config file default "conf.toml")
* -p (specify the packageName in GOPATH)

See `goprecheck --help`

### Run

```
goprecheck -p github.com/caibirdme/goprecheck
```

