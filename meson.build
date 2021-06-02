project(
	'gnss_share',
	version : '0.1',
	meson_version : '>= 0.51.0',
)

pkgdatadir = join_paths(get_option('prefix'), get_option('datadir'), meson.project_name())
appsdir = join_paths(get_option('prefix'), get_option('datadir'), 'applications')
moduledir = join_paths(pkgdatadir, meson.project_name())

conf = configuration_data()
conf.set('VERSION', meson.project_version())
conf.set('pkgdatadir', pkgdatadir)

# exe
configure_file(
	input : meson.project_name() + '/' + meson.project_name() + '.in',
	output : meson.project_name(),
	configuration : conf,
	install : true,
	install_dir : get_option('bindir')
)

# TODO: add option for installing systemd or openrc service files

pkgsources = [
  'gnss_share/logger.py',
  'gnss_share/main.py',
  'gnss_share/stm_agps.py',
  'gnss_share/stm_agps_serial.py',
]
install_data(pkgsources, install_dir : moduledir)

confsources = [
  'gnss_share.conf',
]
install_data(confsources, install_dir : get_option('sysconfdir'))
