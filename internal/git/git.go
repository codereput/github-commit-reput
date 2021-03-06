package git

import (
	"bytes"
	"fmt"
	"github.com/rs/zerolog/log"
	ssh2 "golang.org/x/crypto/ssh"
	goGit "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/config"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"gopkg.in/src-d/go-git.v4/plumbing/transport/ssh"
	"math/rand"
	"os/exec"
	"time"
)

var (
	repo           *goGit.Repository
	auth           *ssh.PublicKeys
	repoPath       string
	untrackedFile  int
	commitQueueMin int
	commitQueueMax int
	commitQueue    int
)

func InitRepo(path, gitRepo string, key []byte, queueMin, queueMax int) error {
	repoPath = path
	untrackedFile = 0
	commitQueueMin = queueMin
	commitQueueMax = queueMax
	commitQueue = calculateNewCommitQueue()
	var err error
	repo, err = goGit.PlainInit(path, false)
	if err != nil {
		if err == goGit.ErrRepositoryAlreadyExists { // repo already initiated
			repo, err = goGit.PlainOpen(path)
			if err := generateAuth(key); err != nil {
				log.Error().Err(err).Msgf("Error generating key")
				return err
			}

			_ = pullRepoIfExist()
			return nil
		} else {
			log.Error().Err(err).Msgf("Error initiating repository")
		}
		return err
	}

	// repo need to be initiated
	_, err = repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{fmt.Sprintf("git@github.com:%v.git", gitRepo)},
	})
	if err != nil {
		log.Error().Err(err).Msgf("Error creating remote repository config")
		return err
	}

	if err := generateAuth(key); err != nil {
		log.Error().Err(err).Msgf("Error generating key")
		return err
	}

	_ = pullRepoIfExist()
	return nil
}

func calculateNewCommitQueue() int {
	rand.Seed(time.Now().UnixNano())
	return rand.Intn(commitQueueMax-commitQueueMin+1) + commitQueueMin
}

func runCommand(arg string) {
	cmd := exec.Command("sh", "-c", arg)
	cmd.Dir = repoPath
	var out, outErr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &outErr
	if err := cmd.Run(); err != nil {
		log.Error().Err(err).Msgf("Cannot run command - %v - %v", out.String(), outErr.String())
	}

}

// very ugly but go-git does not support sparse checkout and we don't want to download the whole repo
func hackSparseCheckout() {
	runCommand("git config core.sparsecheckout true")
	runCommand("mkdir .git/info")
	runCommand("echo '/*' > .git/info/sparse-checkout")
}

func generateAuth(key []byte) error {
	var err error
	auth, err = ssh.NewPublicKeys("git", key, "")
	if err != nil {
		log.Error().Err(err).Msgf("Error generating public key")
		return err
	}
	auth.HostKeyCallback = ssh2.InsecureIgnoreHostKey()
	return nil
}

func pullRepoIfExist() error {
	// TODO does not work with go-git at the moment...
	hackSparseCheckout()

	workTree, err := repo.Worktree()
	if err != nil {
		log.Error().Err(err).Msgf("Error getting WorkTree")
		return err
	}

	err = workTree.Pull(&goGit.PullOptions{Auth: auth})
	if err != nil {
		log.Error().Err(err).Msgf("Error pulling the repository - Maybe it is empty?")
		return err
	}

	return nil
}

func CommitAndPushRepo(username, email string) error {
	workTree, err := repo.Worktree()
	if err != nil {
		log.Error().Err(err).Msgf("Error getting WorkTree")
		return err
	}

	status, err := workTree.Status()
	if err != nil {
		log.Error().Err(err).Msgf("Error retrieving status from workTree")
		return err
	}

	if status.IsClean() { // nothing to do
		log.Debug().Msg("Git status clean -> nothing to commit")
		return nil
	} else if untrackedFile < commitQueue {
		log.Debug().Msgf("UntrackedFile %v < commitQueue %v", untrackedFile, commitQueue)
		untrackedFile++
	} else {
		_, err = workTree.Add(".") // add everything to the staging area
		if err != nil {
			log.Error().Err(err).Msgf("Error adding new files to the staging area")
			return err
		}

		_, err = workTree.Commit(fmt.Sprintf("New content from commit-reput - %v", time.Now().Format("2006-01-02 15:04:05")), &goGit.CommitOptions{
			Author: &object.Signature{
				Name:  username,
				Email: email,
				When:  time.Now(),
			},
		})
		if err != nil {
			log.Error().Err(err).Msgf("Error committing the staging area to the repository")
			return err
		}

		err = repo.Push(&goGit.PushOptions{Auth: auth})
		if err != nil {
			log.Error().Err(err).Msgf("Error pushing the repository")
			return err
		}

		log.Info().Msgf("Successfully pushed %v files  to the repository", untrackedFile)
		untrackedFile = 0
		commitQueue = calculateNewCommitQueue() // we reset the commitQueue to a new number of files
	}

	return err
}
