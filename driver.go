package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/op/go-logging"

	"github.com/docker/go-plugins-helpers/volume"
)

const (
	PluginDataDir   = "/var/lib/docker-lvm-volume/metadata/"
	DriverCacheFile = "/var/lib/docker-lvm-volume/metadata/cache.json"
	LvmVolumeDir    = "/var/lib/docker-lvm-volume/volumes/"
	LvmConfigFile   = "/var/lib/docker-lvm-volume/lvm-volume-plugin.ini"
)

type LvmPersistDriver struct {
	Volumes     map[string]string //key:volume name,value:volume device name
	Mutex       *sync.Mutex
	Name        string
	Mounts      map[string]string // key:volume name, value: mountpoint ids
	VgName      string
	MountCounts map[string]int
}

var log = logging.MustGetLogger("example")

var format = logging.MustStringFormatter(
	`%{time:15:04:05.000} %{shortfunc} %{level:.4s}:%{message}`,
)

func NewLvmPersistDriver() *LvmPersistDriver {

	logFile, err := os.OpenFile("/var/log/docker-volume-plugins.log", os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		log.Error(err)
	}
	backend2 := logging.NewLogBackend(logFile, "", 0)

	backend2Formatter := logging.NewBackendFormatter(backend2, format)
	logging.SetBackend(backend2Formatter)
	logging.SetLevel(logging.INFO, "")

	log.Info("Starting... ")
	os.Mkdir(PluginDataDir, 0750)
	os.Mkdir(LvmVolumeDir, 0750)
	driver := initialCache()

	log.Info("Found %s volumes on startup\n", strconv.Itoa(len(driver.Volumes)))
	return &driver
}

func (driver *LvmPersistDriver) Get(req volume.Request) volume.Response {
	log.Info("list volume ")

	if driver.exists(req.Name) {
		log.Error("Found %s\n", req.Name)
		return volume.Response{
			Volume: driver.volume(req.Name),
		}
	}
	return volume.Response{
		Err: fmt.Sprintf("No volume found with the name %s", req.Name),
	}
}

func (driver *LvmPersistDriver) List(req volume.Request) volume.Response {
	log.Info("List Called... ")

	var volumes []*volume.Volume
	for name, _ := range driver.Volumes {
		volumes = append(volumes, driver.volume(name))
	}

	log.Info("Found %s volumes\n", strconv.Itoa(len(volumes)))

	return volume.Response{
		Volumes: volumes,
	}
}

/*create lv for this volume,but not mount to host dir
 */
func (driver *LvmPersistDriver) Create(req volume.Request) volume.Response {
	driver.Mutex.Lock()
	defer func() {
		driver.Mutex.Unlock()
		if r := recover(); r != nil {
			debug.PrintStack()
			//			return volume.Response{Err: "create volume failed"}

		}
	}()
	log.Info("Create Volume")
	volumeSize, ok := req.Options["size"]
	if !ok || (ok && volumeSize == "") {
		log.Info("The volume %s size is zero,use default 2G", req.Name)
		volumeSize = "2G"
	}
	if driver.exists(req.Name) {
		return volume.Response{Err: fmt.Sprintf("The volume %s already exists", req.Name)}
	}

	/* create lv from vg,mount lv to mountpoint and write to /etc/fstab
	 */
	//1. create lv:lvcreate -L $lvsize -n $lvname $vgname -y
	vgName, vgOk := req.Options["vg"]
	if !vgOk || (vgOk && vgName == "") {
		log.Info("There is no vg info in req use default vg")
		vgName = driver.VgName
	}

	cmdArgs := []string{"-n", req.Name}
	cmdArgs = append(cmdArgs, "-L", volumeSize)
	cmdArgs = append(cmdArgs, vgName)
	cmd := exec.Command("lvcreate", cmdArgs...)
	_, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("create lv from vg error", err)
		return volume.Response{Err: "create lv failed"}
	}

	lvdiskname := fmt.Sprintf("/dev/%s/%s", vgName, req.Name)
	//2. format lv
	cmd = exec.Command("mkfs.xfs", lvdiskname)
	_, err = cmd.CombinedOutput()
	if err != nil {
		log.Error("format lv failed", err)
		return volume.Response{Err: "format lv failed"}
	}
	//3.persist mount lv to mountPoint
	mountPoint := LvmVolumeDir + req.Name
	cmdArgs = []string{"-p", mountPoint}
	cmd = exec.Command("mkdir", cmdArgs...)
	_, err = cmd.CombinedOutput()
	if err != nil {
		log.Error("mkdir local mountpoint failed", err)
		return volume.Response{Err: "mkdir mountpoint failedss"}
	}
	err = syscall.Chmod(mountPoint, 0750)
	if err != nil {
		log.Error("chmod mountpoint failed", err)
		return volume.Response{Err: "mkdir mountpoint failedss"}
	}
	cmdArgs = []string{lvdiskname, mountPoint}
	log.Info(cmdArgs)
	cmd = exec.Command("mount", cmdArgs...)
	if _, err = cmd.CombinedOutput(); err != nil {
		log.Error("mount lv failed", err)
		return volume.Response{Err: fmt.Sprintf("volum mount failed")}
	}

	//bug:syscall mount to another namspace
	//	err = syscall.Mount(lvdiskname, mountPoint, "xfs", syscall.MS_NOSUID|syscall.MS_STRICTATIME, "")

	//	if err != nil {
	//		log.Error("mount lv failed", err)
	//		return volume.Response{Err: "mount lv failed"}
	//	}
	content := lvdiskname + " " + mountPoint + " xfs defaults 0 1"
	updateFstab(content, false)

	//3 persist to data dir

	//	cmd = exec.Command("touch", PluginDataDir+req.Name)
	//	_, err = cmd.CombinedOutput()
	//	if err != nil {
	//		fmt.Println("persist voulme info failed", err)
	//		return volume.Response{Err: fmt.Sprintf("internal error")}
	//	}
	//update cache info
	driver.Volumes[req.Name] = lvdiskname
	driver.Mounts[req.Name] = mountPoint
	driver.UpdateCacheFile()
	return volume.Response{Mountpoint: mountPoint}
}

func (driver *LvmPersistDriver) Remove(req volume.Request) volume.Response {
	log.Info("Remove Volume ")
	driver.Mutex.Lock()
	defer func() {
		driver.Mutex.Unlock()
		if r := recover(); r != nil {
			debug.PrintStack()
			log.Error("Remove volume err ", r)
			//return volume.Response{Err: "remove volume failed"}
		}
	}()
	deviceName := driver.Volumes[req.Name]
	//0 check
	if driver.MountCounts[req.Name] > 0 {
		log.Error("volume is mounted,can not remove ")
		return volume.Response{Err: "remove volume failed"}
	}
	//1 umount
	cmdArgs := []string{"-l",driver.Mounts[req.Name]}
	cmd := exec.Command("umount", cmdArgs...)
	if _, err := cmd.CombinedOutput(); err != nil {
		log.Error("umount lv failed", err)
		//return volume.Response{Err: fmt.Sprintf("volum umount failed")}
	}

	//2 update fstab
	content := deviceName + " " + driver.Mounts[req.Name] + " xfs defaults 0 1"
	updateFstab(content, true)
	//3. remove from vg  $lvdiskname -f
	cmdArgs = []string{deviceName, "-f"}
	cmd = exec.Command("lvremove", cmdArgs...)
	if _, err:= cmd.CombinedOutput(); err != nil {
		log.Error("remove lv  failed", err)
		//return volume.Response{Err: "remove lv failed"}
	}
	//remove local dir
	err := os.RemoveAll(driver.Mounts[req.Name])
	if err != nil {
		log.Error("remove voulme info failed", err)
		//return volume.Response{Err: "remove local dir failed"}
	}
	//1.remove from cache
	delete(driver.Volumes, req.Name)
	delete(driver.Mounts, req.Name)
	driver.UpdateCacheFile()
	return volume.Response{}
}

func (driver *LvmPersistDriver) Mount(req volume.Request) volume.Response {
	driver.Mutex.Lock()
	defer driver.Mutex.Unlock()
	log.Info("Mount Called... ")
	mountPoint := driver.Mounts[req.Name]
	if mountPoint == "" {
		return volume.Response{Err: fmt.Sprintf("The volume %s not exist", req.Name)}
	}
	driver.MountCounts[req.Name] = driver.MountCounts[req.Name] + 1
	return volume.Response{Mountpoint: mountPoint}
}

func (driver *LvmPersistDriver) Path(req volume.Request) volume.Response {
	mountPoint := driver.Mounts[req.Name]
	if mountPoint == "" {
		return volume.Response{Err: fmt.Sprintf("The volume %s not exist", req.Name)}
	}
	return volume.Response{Mountpoint: mountPoint}
}

func (driver *LvmPersistDriver) Unmount(req volume.Request) volume.Response {
	driver.Mutex.Lock()
	defer driver.Mutex.Unlock()
	log.Info("Unmount Called... ")
	_, ok := driver.Volumes[req.Name]
	if !ok {
		return volume.Response{Err: fmt.Sprintf("The volume %s not exist", req.Name)}
	}
	driver.MountCounts[req.Name] = driver.MountCounts[req.Name] - 1
	driver.UpdateCacheFile()
	return volume.Response{}
}
func (driver *LvmPersistDriver) Capabilities(req volume.Request) volume.Response {
	return volume.Response{
		Capabilities: volume.Capability{Scope: "global"},
	}

}

func initialCache() LvmPersistDriver {
	driver := LvmPersistDriver{
		Mutex: &sync.Mutex{},
		Name:  "LVM",
	}

	if _, err := os.Stat(DriverCacheFile); err == nil {
		data := LvmPersistDriver{}
		bytes, _ := ioutil.ReadFile(DriverCacheFile)
		json.Unmarshal(bytes, &data)
		driver.Volumes = data.Volumes
		driver.Mounts = data.Mounts
		driver.MountCounts = data.MountCounts
	}
	if driver.Mounts == nil {
		driver.Mounts = make(map[string]string)
	}
	if driver.Volumes == nil {
		driver.Volumes = make(map[string]string)
	}
	if driver.MountCounts == nil {
		driver.MountCounts = make(map[string]int)
	}
	driver.VgName = "VG0"

	return driver
}

func (driver *LvmPersistDriver) UpdateCacheFile() {
	log.Info("UpdateCacheFile")
	data, err := json.Marshal(driver)
	if err != nil {
		log.Error(err)
	}
	//	fmt.Println("cache data is %s", string(data))
	err = ioutil.WriteFile(DriverCacheFile, data, 0755)
	if err != nil {
		log.Error("update cache filed failed", err)
	}
}

func (driver *LvmPersistDriver) volume(name string) *volume.Volume {
	return &volume.Volume{Name: name,
		Mountpoint: LvmVolumeDir + name}
}

func (driver *LvmPersistDriver) exists(name string) bool {
	return driver.Volumes[name] != ""
}

////append or delete lv info in /etc/fstab
func updateFstab(content string, isDelete bool) {

	//backup /etc/fstab
	cmdArgs := []string{"/etc/fstab", "/etc/fstab.back"}
	cmd := exec.Command("cp", cmdArgs...)
	if _, err1 := cmd.CombinedOutput(); err1 != nil {
		log.Error("back fstab failed", err1)
	}

	if isDelete {
		removeLineFrom(content)
		return
	}

	f, err := os.OpenFile("/etc/fstab", os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Error("open fstab failed", err)
		return
	}
	defer f.Close()

	_, err = f.WriteString(content + "\n")
	if err != nil {
		log.Error("update fstab failed", err)
	}
}

func removeLineFrom(line string) {
	input, err := ioutil.ReadFile("/etc/fstab")
	log.Info("delte line is ", line)
	if err != nil {
		log.Error("read fstab failed", err)
		return
	}

	lines := strings.Split(string(input), "\n")
	lineIndex := 0
	found := false
	for i, value := range lines {
		if strings.Contains(value, line) {

			lineIndex = i
			found = true
			break
		}
	}
	if found {
		lines = append(lines[:lineIndex], lines[lineIndex+1:]...)
	}
	output := strings.Join(lines, "\n")
	err = ioutil.WriteFile("/etc/fstab", []byte(output), 0644)
	if err != nil {
		log.Error("read fstab failed", err)
	}
}
