pipeline {
    agent none

    options {
        disableConcurrentBuilds()
        timeout(time: 45, unit: 'MINUTES')
    }

    environment {
        CGO_ENABLED = '0'
        GOFLAGS = '-buildvcs=false'
    }

    stages {
        stage('Build') {
            agent { docker { image 'casjaysdev/go:latest'; args '--user 0:0' } }
            steps { sh 'go build ./...' }
        }
        stage('Test') {
            agent { docker { image 'casjaysdev/go:latest'; args '--user 0:0' } }
            steps {
                sh '''
                    JOB_DIR=$(printf '%s' "$JOB_NAME" | tr '/' '-')
                    mkdir -p "${TMPDIR:-/tmp}/$JOB_DIR"
                    COVDIR=$(mktemp -d "${TMPDIR:-/tmp}/$JOB_DIR/gitmessages-XXXXXX")
                    go test -p 1 -cover -coverprofile="$COVDIR/coverage.out" ./...
                    PCT=$(go tool cover -func="$COVDIR/coverage.out" | awk '/^total:/ {gsub("%", "", $3); print int($3)}')
                    test "$PCT" -ge 60 || { echo "ERROR: coverage ${PCT}% is below 60%"; exit 1; }
                '''
            }
        }
        stage('Security') {
            parallel {
                stage('Secret Scan') {
                    agent any
                    steps {
                        sh '''
                            git fetch --unshallow 2>/dev/null || true
                            docker run --rm --name "trufflehog-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" -v "$PWD:/repo" trufflesecurity/trufflehog:latest git file:///repo --results=verified,unknown --exclude-paths=.github/trufflehog-exclude.txt --fail
                        '''
                    }
                }
                stage('Vulnerability Scan') {
                    agent { docker { image 'casjaysdev/go:latest'; args '--user 0:0' } }
                    steps { sh 'govulncheck ./...' }
                }
                stage('Workflow Policy') {
                    agent any
                    steps {
                        sh '''
                            if grep -nE 'pull_request_target|uses:[^@]+@(main|master|v[0-9])' .github/workflows/*.yml; then
                                echo 'ERROR: prohibited workflow trigger or unpinned action'
                                exit 1
                            fi
                        '''
                    }
                }
            }
        }
        stage('Release') {
            when { tag 'v*' }
            agent { docker { image 'casjaysdev/go:latest'; args '--user 0:0' } }
            steps {
                sh '''
                    VERSION="${TAG_NAME#v}"
                    mkdir -p dist
                    for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64 freebsd/amd64 freebsd/arm64; do
                        GOOS="${target%/*}" GOARCH="${target#*/}" CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X main.Version=$VERSION -X main.CommitID=$(git rev-parse --short=7 HEAD) -X main.BuildEpoch=$(date -u +%s)" -o "dist/gitmessages-${target%/*}-${target#*/}" ./src
                    done
                    cd dist && sha256sum gitmessages-* > sha256.txt
                '''
                archiveArtifacts artifacts: 'dist/*', fingerprint: true
            }
        }
    }
}
