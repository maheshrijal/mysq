#!/usr/bin/env fish
# Local playground for the current build.
#
#   tools/dev-env.fish up      build bin/mysq, start MySQL in Docker, start background load
#   tools/dev-env.fish env     print connection exports; apply with: tools/dev-env.fish env | source
#   tools/dev-env.fish down    stop load and MySQL
#
# Reuses docker-compose.e2e.yml and the users from test/e2e/init.sql.
# MYSQ_MYSQL_PORT (default 33306) picks the published port.
# MYSQ_DEV_LOAD_DURATION (default 2h) bounds the background load generator.
# MYSQ_DEV_OPERATOR=1 makes `env` export the operator account, which can kill sessions.

set -l root (path resolve (dirname (status filename))/..)
set -l project mysq-dev
set -l port $MYSQ_MYSQL_PORT
test -n "$port"; or set port 33306
set -l duration $MYSQ_DEV_LOAD_DURATION
test -n "$duration"; or set duration 2h
set -l state $root/bin/dev-env
set -l compose docker compose --project-name $project -f $root/docker-compose.e2e.yml

set -l monitor_url "mysql://mysq_monitor:mysq-monitor-test@127.0.0.1:$port/app"
set -l operator_url "mysql://mysq_operator:mysq-operator-test@127.0.0.1:$port/app"
set -l load_dsn "loadgen:mysq-load-test@tcp(127.0.0.1:$port)/app?parseTime=true"

# Signals the load generator recorded by `up`, and only that binary.
function stop_load --no-scope-shadowing
    if test -f $state/load.pid
        set -l pid (cat $state/load.pid)
        if test -n "$pid"; and string match -q -- "$state/load *" (ps -p $pid -o command= 2>/dev/null)
            kill $pid
        end
        rm -f $state/load.pid
    end
end

switch "$argv[1]"
    case up
        cd $root; or exit 1
        mkdir -p $state
        set -l build_version (git describe --always --dirty 2>/dev/null)
        test -n "$build_version"; or set build_version unknown
        go build -trimpath -ldflags "-X main.version=dev-$build_version" -o bin/mysq ./cmd/mysq; or exit 1
        go build -trimpath -o $state/load ./test/e2e/load; or exit 1
        stop_load
        MYSQ_MYSQL_PORT=$port $compose up -d --wait; or exit 1
        $state/load --dsn $load_dsn --duration $duration >$state/load.log 2>&1 &
        set -l pid $last_pid
        echo $pid >$state/load.pid
        disown
        set -l ready 0
        for attempt in (seq 120)
            if grep -q '^load ready$' $state/load.log
                set ready 1
                break
            end
            if not kill -0 $pid 2>/dev/null
                cat $state/load.log >&2
                echo "load generator exited before becoming ready; MySQL is still up, run tools/dev-env.fish down to stop it" >&2
                rm -f $state/load.pid
                exit 1
            end
            sleep 0.5
        end
        if test $ready -eq 0
            stop_load
            echo "timed out waiting for the load generator; MySQL is still up, run tools/dev-env.fish down to stop it" >&2
            exit 1
        end
        echo "MySQL ready on 127.0.0.1:$port; load generator running for $duration (log: $state/load.log)"
        echo
        echo "Apply connection exports in this shell:"
        echo "  tools/dev-env.fish env | source"
        echo "Then:"
        echo "  mysq tui"
        echo "  mysq queries"
    case env
        set -l url $monitor_url
        if test "$MYSQ_DEV_OPERATOR" = 1
            set url $operator_url
        end
        set -l bin (string escape -- $root/bin)
        echo "set -gx MYSQ_DATABASE_URL "(string escape -- $url)
        echo "if not contains -- $bin \$PATH; set -gx PATH $bin \$PATH; end"
    case down
        stop_load
        MYSQ_MYSQL_PORT=$port $compose down --remove-orphans
    case '*'
        echo "usage: tools/dev-env.fish up|env|down" >&2
        exit 2
end
