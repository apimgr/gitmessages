pipeline {
  agent {
    docker {
      image 'casjaysdev/go:latest'
      args '-e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false'
    }
  }
  stages {
    stage('Test') {
      steps {
        sh 'go test -p 1 -cover ./...'
      }
    }
    stage('Vet') {
      steps {
        sh 'go vet ./...'
      }
    }
    stage('Staticcheck') {
      steps {
        sh 'staticcheck ./...'
      }
    }
    stage('Build') {
      steps {
        sh 'go build -buildvcs=false ./...'
      }
    }
    stage('Vulnerability scan') {
      steps {
        sh 'govulncheck ./...'
      }
    }
  }
}
