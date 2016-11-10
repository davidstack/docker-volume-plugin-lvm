package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	_ "github.com/Unknwon/goconfig"
	"github.com/docker/go-plugins-helpers/volume"
)

const (
	PluginDataDir   = "/var/lib/docker-lvm-volume/metadata/"
	DriverCacheFile = "/var/lib/docker-lvm-volume/metadata/cache.json"
	LvmVolumeDir    = "/var/lib/docker-lvm-volume/volumes/"
	LvmConfigFile   = "/var/lib/docker-lvm-volume/lvm-volume-plugin.ini"
)

type LvmPersistDriver struct {
	Volumes map[string]string //key:volume name,value:volume device name
	Mutex   *sync.Mutex
	Name    string
	Mounts  map[string]string // key:volume name, value: mountpoint ids
	VgName  string
	//MountCounts map[string]int64
}

func NewLvmPersistDriver() *LvmPersistDriver {
	fmt.Println("Starting... ")
	os.Mkdir(PluginDataDir, 0700)
	os.Mkdir(LvmVolumeDir, 0700)
	driver := initialCache()

	fmt.Printf("Found %s volumes on startup\n", strconv.Itoa(len(driver.Volumes)))
	return &driver
}

func (driver *LvmPersistDriver) Get(req volume.Request) volume.Response {
	fmt.Println("list volume ")

	if driver.exists(req.Name) {
		fmt.Println("Found %s\n", req.Name)
		return volume.Response{
			Volume: driver.volume(req.Name),
		}
	}
	return volume.Response{
		Err: fmt.Sprintf("No volume found with the name %s", req.Name),
	}
}

func (driver *LvmPersistDriver) List(req volume.Request) volume.Response {
	fmt.Println("List Called... ")

	var volumes []*volume.Volume
	for name, _ := range driver.Volumes {
		volumes = append(volumes, driver.volume(name))
	}

	fmt.Printf("Found %s volumes\n", strconv.Itoa(len(volumes)))

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
			//TODO clean garbage data
			//return volume.Response{Err: "create volume failed"}

		}
	}()
	fmt.Print("Create Called... ")
	volumeSize, ok := req.Options["size"]
	if !ok || (ok && volumeSize == "") {
		fmt.Sprintf("The volume %s size is zero,use default 2G", req.Name)
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
		fmt.Sprintf("The no vg info in req use default vg")
		vgName = driver.VgName
	}

	cmdArgs := []string{"-n", req.Name}
	cmdArgs = append(cmdArgs, "-L", volumeSize)
	cmdArgs = append(cmdArgs, vgName)
	fmt.Println(cmdArgs)
	cmd := exec.Command("lvcreate", cmdArgs...)
	_, err := cmd.CombinedOutput()

	if err != nil {
		fmt.Print("create lv from vg error", err)
		panic("create lv from vg error")
	}

	lvdiskname := fmt.Sprintf("/dev/%s/%s", vgName, req.Name)
	//2. format lv
	cmd = exec.Command("mkfs.xfs", lvdiskname)
	_, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Println("format lv failed", err)
		return volume.Response{Err: fmt.Sprintf("format volume %s failed", req.Name)}
	}
	//3.persist mount lv to mountPoint
	mountPoint := LvmVolumeDir + req.Name

	os.Mkdir(mountPoint, 0644)
	cmdArgs = []string{lvdiskname, mountPoint}
	fmt.Println(cmdArgs)
	cmd = exec.Command("mount", cmdArgs...)
	if _, err = cmd.CombinedOutput(); err != nil {
		fmt.Println("mount lv failed", err)
		return volume.Response{Err: fmt.Sprintf("volum mount failed")}
	} else {
		//update /etc/fstab
		cmdArgs = []string{"/etc/fstab", "/etc/fstab.back"}
		cmd = exec.Command("cp", cmdArgs...)
		if _, err1 := cmd.CombinedOutput(); err1 != nil {
			fmt.Println("back fstab failed", err1)
		}
		content := lvdiskname + " " + mountPoint + " xfs defaults 0 1"
		updateFstab(content, false)
	}

	//3 persist to data dir
	cmd = exec.Command("touch", PluginDataDir+req.Name)
	_, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Println("persist voulme info failed", err)
		return volume.Response{Err: fmt.Sprintf("internal error")}
	}

	fmt.Println("disk name is %s", lvdiskname)
	driver.Volumes[req.Name] = lvdiskname
	driver.Mounts[req.Name] = mountPoint
	driver.UpdateCacheFile()
	return volume.Response{Mountpoint: mountPoint}
}

func (driver *LvmPersistDriver) Remove(req volume.Request) volume.Response {
	fmt.Println("Remove Called... ")
	driver.Mutex.Lock()
	defer func() {
		driver.Mutex.Unlock()
		if r := recover(); r != nil {
			fmt.Println("Remove volume err ", r)
			//
		}
	}()
	deviceName := driver.Volumes[req.Name]
	//0 check any mount info
	//	if _, ok := driver.Mounts[req.Name]; ok {
	//		return volume.Response{Err: fmt.Sprintf("this volume has mount point ,can not remove")}
	//	}

	//umount
	cmd := exec.Command("umount", deviceName)
	if _, err := cmd.CombinedOutput(); err != nil {
		fmt.Println("umount lv failed", err)
		return volume.Response{Err: fmt.Sprintf("remove volume %s failed", req.Name)}
	}

	//update fstab
	content := deviceName + " " + driver.Mounts[req.Name] + " xfs defaults 0 1"
	updateFstab(content, true)
	//2. remove from vg  $lvdiskname -f
	cmdArgs := []string{deviceName, "-f"}
	cmd = exec.Command("lvremove", cmdArgs...)
	if _, err := cmd.CombinedOutput(); err != nil {
		fmt.Println("remove voulme info failed", err)
		return volume.Response{Err: "remove volume failed"}
	}
	//remove locl dir
	os.RemoveAll(driver.Mounts[req.Name])
	//3. remove from persist data
	cmd = exec.Command("rm", PluginDataDir+req.Name)
	_, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Print("remove voulme info failed", err)
		return volume.Response{Err: "remove volume failed"}
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
	fmt.Println("Mount Called... ")
	devicename := driver.Volumes[req.Name]
	if devicename == "" {
		return volume.Response{Err: fmt.Sprintf("The volume %s not exist", req.Name)}
	}
	fmt.Println(devicename)
	mountPoint := LvmVolumeDir + req.Name

	driver.UpdateCacheFile()
	return volume.Response{Mountpoint: mountPoint}
}

func (driver *LvmPersistDriver) Path(req volume.Request) volume.Response {
	devicename := driver.Volumes[req.Name]

	if devicename != "" {
		return volume.Response{Err: fmt.Sprintf("The volume %s not exist", req.Name)}
	}
	return volume.Response{Mountpoint: LvmVolumeDir + req.Name}
}

func (driver *LvmPersistDriver) Unmount(req volume.Request) volume.Response {
	driver.Mutex.Lock()
	defer driver.Mutex.Unlock()
	fmt.Println("Unmount Called... ")
	_, ok := driver.Volumes[req.Name]
	if !ok {
		return volume.Response{Err: fmt.Sprintf("The volume %s not exist", req.Name)}
	}

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
	}
	if driver.Mounts == nil {
		driver.Mounts = make(map[string]string)
	}
	if driver.Volumes == nil {
		driver.Volumes = make(map[string]string)
	}

	driver.VgName = "VG0"

	return driver
}

func (driver *LvmPersistDriver) UpdateCacheFile() {
	fmt.Println("UpdateCacheFile")
	data, err := json.Marshal(driver)
	if err != nil {
		fmt.Println(err)
	}
	//	fmt.Println("cache data is %s", string(data))
	err = ioutil.WriteFile(DriverCacheFile, data, 0644)
	if err != nil {
		fmt.Println("update cache filed failed", err)
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
	if isDelete {
		removeLineFrom(content)
		return
	}
	f, err := os.OpenFile("/etc/fstab", os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Println("open fstab failed", err)
		return
	}
	defer f.Close()

	_, err = f.WriteString(content + "\n")
	if err != nil {
		fmt.Println("update fstab failed", err)
	}
}

func removeLineFrom(line string) {
	input, err := ioutil.ReadFile("/etc/fstab")
	fmt.Println("delte line is ", line)
	if err != nil {
		fmt.Println("read fstab failed", err)
		return
	}

	lines := strings.Split(string(input), "\n")
	lineIndex := 0
	found := false
	for i, value := range lines {
		if strings.Contains(value, line) {
			fmt.Println("found")
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
		fmt.Println("read fstab failed", err)
	}
}
