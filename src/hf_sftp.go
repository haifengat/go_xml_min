package src

import (
	"fmt"
	"net"
	"os"
	"path"
	"time"

	"github.com/pkg/sftp"
	"github.com/sirupsen/logrus"

	// log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

// HfSftp 结构
type HfSftp struct {
	sshClient *ssh.Client
	client    *sftp.Client
}

// NewHfSftp 创建sftp实例
func NewHfSftp(host string, port int, user string, pwd string) (*HfSftp, error) {
	sshConfig := &ssh.ClientConfig{
		User: user,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
		Auth: []ssh.AuthMethod{
			ssh.Password(pwd),
		},
		Timeout: 0,
	}

	//sshConfig.SetDefaults()
	var (
		e   HfSftp
		err error
	)
	if e.sshClient, err = ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), sshConfig); err == nil {
		// log.Info("Successfully connected to ssh server.")
		// open an SFTP session over an existing ssh connection.
		e.client, err = sftp.NewClient(e.sshClient)
	}
	return &e, err
}

// Close 关闭
func (e *HfSftp) Close() error {
	defer e.sshClient.Close()
	return e.client.Close()
}

// GetFileNames 取指定路径下的文件名
func (e *HfSftp) GetFileNames(remotePath string) ([]string, error) {
	var (
		files []string
		err   error
	)
	w := e.client.Walk(remotePath)
	for w.Step() {
		//w.SkipDir()
		if w.Err() != nil {
			continue
		}
		if w.Stat().IsDir() {
			continue
		}
		_, file := path.Split(w.Path())
		files = append(files[:], file)
	}
	return files, err
}

// GetFileState 取远程文件信息
func (e *HfSftp) GetFileState(fileName string) (os.FileInfo, error) {
	return e.client.Stat(fileName)
}

// Download 下载文件
func (e *HfSftp) Download(remoteFileFullName, localPath string) (err error) {
	var info os.FileInfo
	if info, err = e.GetFileState(remoteFileFullName); err == nil {
		// 判断文件写入完成
		for {
			preSize := info.Size()
			time.Sleep(10 * time.Second)
			info, _ = e.GetFileState(remoteFileFullName)
			if preSize == info.Size() {
				break
			}
		}
		var srcFile *sftp.File
		if srcFile, err = e.client.Open(remoteFileFullName); err == nil {
			defer srcFile.Close()
			var dstFile *os.File
			os.MkdirAll(localPath, os.ModePerm)
			if dstFile, err = os.Create(path.Join(localPath, path.Base(remoteFileFullName))); err == nil {
				logrus.Info(remoteFileFullName, " reading...")
				defer dstFile.Close()
				buf := make([]byte, 1024*1024*100)
				for n, _ := srcFile.Read(buf); n > 0; n, _ = srcFile.Read(buf) {
					dstFile.Write(buf[0:n])
				}
				logrus.Info(remoteFileFullName, " download to ", localPath, " succeed, size: ", info.Size(), ".")
			}
		}
	}
	return err
}
