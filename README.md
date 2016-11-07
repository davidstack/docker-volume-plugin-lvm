基于LVM 实现Docker 宿主机的Volume分配，目前只提供基于二进制文件运行  

1、安装 (Ubuntu 14.04 环境测试OK)  

    1.0 Docker 宿主机已经使用LVM创建VG，获取VGName  
    
    1.1 git clone https://github.com/davidstack/docker-volume-plugin-lvm.git   
    
    1.2 cp conf/lvm-volume-plugin.ini /var/lib/docker-lvm-volume/lvm-volume-plugin.ini  
    
    1.3 在文件 /var/lib/docker-lvm-volume/lvm-volume-plugin.ini 修改VGName  
    
2、 运行 ./docker-volume-plugin-lvm   

3、测试   

   3.1 创建卷并指定卷大小：docker volume create --name wangdk3 -d LVM -o size=1G   
   
   3.2 运行Container使用已经存在的卷：docker run -itd -v wangdk3:/mnt ubuntu:15.04 bash   
   
   3.3 创建Container 同时创建卷并挂载，docker create -it -v wangdk2:/mnt --volume-driver=LVM ubuntu:15.04 bash  卷大小使用默认值2G  
  PS:
   目前不支持在创建Container的时候指定卷大小，使用默认值 2Gb
   
    
