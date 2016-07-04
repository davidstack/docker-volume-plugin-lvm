基于LVM 实现Docker 宿主机的Volume分配，目前只提供基于二进制文件运行＜/br＞
1、安装 (Ubuntu 14.04 环境测试OK)＜/br＞
    1.0 Docker 宿主机已经使用LVM创建VG，获取VGName＜/br＞
    1.1 git clone https://github.com/davidstack/docker-volume-plugin-lvm.git＜/br＞
    1.2 cp conf/lvm-volume-plugin.ini /var/lib/docker-lvm-volume/lvm-volume-plugin.ini＜/br＞
    1.3 在文件 /var/lib/docker-lvm-volume/lvm-volume-plugin.ini 修改VGName＜/br＞
2、 运行 ./docker-volume-plugin-lvm＜/br＞
3、测试＜/br＞
   3.1 创建卷并指定卷大小：docker volume create --name wangdk3 -d LVM -o size=1G＜/br＞
   3.2 运行Container使用已经存在的卷：docker run -itd -v wangdk3:/mnt ubuntu:15.04 bash＜/br＞
   3.3 创建Container 同时创建卷并挂载，docker create -it -v wangdk2:/mnt --volume-driver=LVM ubuntu:15.04 bash  卷大小使用默认值2G＜/br＞
    
