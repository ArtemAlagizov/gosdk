package sdk

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/0chain/gosdk/zboxcore/fileref"
	. "github.com/0chain/gosdk/zboxcore/logger"
	"go.uber.org/zap"
)

type RepairRequest struct {
	listDir           *ListResult
	isRepairCanceled  bool
	localImagePath    string
	localFilePath     string
	statusCB          StatusCallback
	completedCallback func()
	filesRepaired     int
}

type RepairStatusCB struct {
	wg       *sync.WaitGroup
	success  bool
	err      error
	statusCB StatusCallback
}

func (cb *RepairStatusCB) CommitMetaCompleted(request, response string, err error) {
	cb.statusCB.CommitMetaCompleted(request, response, err)
}

func (cb *RepairStatusCB) Started(allocationId, filePath string, op int, totalBytes int) {
	cb.statusCB.Started(allocationId, filePath, op, totalBytes)
}

func (cb *RepairStatusCB) InProgress(allocationId, filePath string, op int, completedBytes int) {
	cb.statusCB.InProgress(allocationId, filePath, op, completedBytes)
}

func (cb *RepairStatusCB) RepairCompleted(filesRepaired int) {
	cb.statusCB.RepairCompleted(filesRepaired)
}

func (cb *RepairStatusCB) RepairCancelled(err error) {
	cb.statusCB.RepairCancelled(err)
}

func (cb *RepairStatusCB) Completed(allocationId, filePath string, filename string, mimetype string, size int, op int) {
	cb.success = true
	cb.statusCB.Completed(allocationId, filePath, filename, mimetype, size, op)
	if op == OpDownload || op == OpCommit {
		defer cb.wg.Done()
	}
}

func (cb *RepairStatusCB) Error(allocationID string, filePath string, op int, err error) {
	cb.success = false
	cb.err = err
	cb.statusCB.Error(allocationID, filePath, op, err)
	cb.wg.Done()
}

func (r *RepairRequest) processRepair(ctx context.Context, a *Allocation) {
	if r.completedCallback != nil {
		defer r.completedCallback()
	}

	if r.isRepairCanceled {
		Logger.Info("Repair Cancelled by the user")
		if r.statusCB != nil {
			r.statusCB.RepairCompleted(r.filesRepaired)
		}
		return
	}

	r.iterateDir(a, r.listDir)

	if r.statusCB != nil {
		r.statusCB.RepairCompleted(r.filesRepaired)
	}

	return
}

func (r *RepairRequest) iterateDir(a *Allocation, dir *ListResult) {
	if dir.Type == fileref.DIRECTORY && len(dir.Children) > 0 {
		for _, childDir := range dir.Children {
			if r.isRepairCanceled {
				Logger.Info("Repair Cancelled by the user")
				if r.statusCB != nil {
					r.statusCB.RepairCompleted(r.filesRepaired)
				}
				return
			}
			r.iterateDir(a, childDir)
		}
	} else if dir.Type == fileref.FILE {
		r.repairFile(a, dir)
	} else {
		Logger.Info("Invalid directory type, No file in the given repair path")
	}
	return
}

func (r *RepairRequest) repairFile(a *Allocation, file *ListResult) {
	if r.isRepairCanceled {
		Logger.Info("Repair Cancelled by the user")
		if r.statusCB != nil {
			r.statusCB.RepairCompleted(r.filesRepaired)
		}
		return
	}

	Logger.Info("Repairing file for the path :", zap.Any("path", file.Path))
	_, repairRequired, _, err := a.RepairRequired(file.Path)
	if err != nil {
		Logger.Error("repair_required_failed", zap.Error(err))
		return
	}

	if repairRequired {
		var wg sync.WaitGroup
		statusCB := &RepairStatusCB{
			wg:       &wg,
			statusCB: r.statusCB,
		}

		localPath := r.getLocalPath(file)

		if !checkFileExists(localPath) {
			wg.Add(1)
			err = a.DownloadFile(localPath, file.Path, statusCB)
			if err != nil {
				Logger.Error("download_file_failed", zap.Error(err))
				return
			}
			wg.Wait()
			if !statusCB.success {
				Logger.Error("Failed to download file for repair, Status call back success failed",
					zap.Any("localpath", localPath), zap.Any("remotepath", file.Path))
				return
			}
			Logger.Info("Download file success for repair", zap.Any("localpath", localPath), zap.Any("remotepath", file.Path))
			statusCB.success = false
		}

		wg.Add(1)
		err = a.RepairFile(localPath, file.Path, statusCB)
		if err != nil {
			Logger.Error("repair_file_failed", zap.Error(err))
			return
		}
		wg.Wait()
		if !statusCB.success {
			Logger.Error("Failed to repair file, Status call back success failed",
				zap.Any("localpath", localPath), zap.Any("remotepath", file.Path))
			return
		}
		Logger.Info("Repair file success", zap.Any("localpath", localPath), zap.Any("remotepath", file.Path))
		r.filesRepaired++
	}

	return
}

func (r *RepairRequest) getLocalPath(file *ListResult) string {
	if strings.Contains(file.MimeType, "image") {
		return r.localImagePath + file.Name
	}
	return r.localFilePath + file.Name
}

func checkFileExists(localPath string) bool {
	info, err := os.Stat(localPath)
	if err != nil {
		return false
	}
	return !info.IsDir()
}