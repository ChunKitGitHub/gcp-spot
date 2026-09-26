package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func runSelfUpdate() {
	if os.Geteuid() != 0 {
		fmt.Println("请使用 sudo relay update 运行以获取系统更新权限。")
		os.Exit(1)
	}

	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		fmt.Printf("不支持的系统架构: %s\n", arch)
		os.Exit(1)
	}

	tarName := fmt.Sprintf("newspot-relay-linux-%s.tar.gz", arch)
	url := fmt.Sprintf("https://raw.githubusercontent.com/ChunKitGitHub/gcp-spot/main/dist/%s", tarName)

	fmt.Printf("⚡ 正在从 GitHub 下载最新版本 (%s)...\n", tarName)
	fmt.Printf("地址: %s\n", url)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("❌ 下载失败: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("❌ GitHub 返回错误 HTTP %d (请确认 gcp-spot 仓库已就绪)\n", resp.StatusCode)
		os.Exit(1)
	}

	tmpDir, err := os.MkdirTemp("", "relay-update-*")
	if err != nil {
		fmt.Printf("❌ 创建临时目录失败: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	gr, err := gzip.NewReader(resp.Body)
	if err != nil {
		fmt.Printf("❌ gzip 解压失败: %v\n", err)
		os.Exit(1)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var newBinPath string
	agentBinDir := "/var/lib/newspot-relay/bin"
	_ = os.MkdirAll(agentBinDir, 0755)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("❌ 读取解压文件失败: %v\n", err)
			os.Exit(1)
		}

		baseName := filepath.Base(header.Name)
		if baseName == "newspot-relay" {
			targetPath := filepath.Join(tmpDir, "newspot-relay")
			f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				fmt.Printf("❌ 写入文件失败: %v\n", err)
				os.Exit(1)
			}
			_, _ = io.Copy(f, tr)
			f.Close()
			newBinPath = targetPath
		} else if strings.HasPrefix(baseName, "spot-agent-") {
			targetPath := filepath.Join(agentBinDir, baseName)
			f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err == nil {
				_, _ = io.Copy(f, tr)
				f.Close()
			}
		}
	}

	if newBinPath == "" {
		fmt.Println("❌ 更新包中未找到 newspot-relay 二进制文件。")
		os.Exit(1)
	}

	destPath := "/usr/local/bin/newspot-relay"
	fmt.Println("正在替换二进制文件...")
	if err := os.Rename(newBinPath, destPath); err != nil {
		srcF, _ := os.Open(newBinPath)
		dstF, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			fmt.Printf("❌ 覆盖目标文件失败: %v\n", err)
			os.Exit(1)
		}
		_, _ = io.Copy(dstF, srcF)
		srcF.Close()
		dstF.Close()
	}
	_ = os.Chmod(destPath, 0755)

	// 创建 relay 快捷方式
	_ = os.Remove("/usr/local/bin/relay")
	_ = os.Symlink(destPath, "/usr/local/bin/relay")

	fmt.Println("正在重启 newspot-relay 服务...")
	_ = exec.Command("systemctl", "daemon-reload").Run()
	if err := exec.Command("systemctl", "restart", "newspot-relay").Run(); err != nil {
		fmt.Printf("⚠️ 重启服务失败: %v (请手动执行 sudo systemctl restart newspot-relay)\n", err)
		return
	}

	fmt.Println("🎉 更新成功！newspot-relay 服务已重新加载并正常运行。")
}
